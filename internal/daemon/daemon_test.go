package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/kind"
	"github.com/clobrano/ghwatch/internal/kind/pr"
	"github.com/clobrano/ghwatch/internal/model"
	"github.com/clobrano/ghwatch/internal/notify"
)

// fakePR parses like the real PR kind but serves items from memory.
type fakePR struct {
	pr.Kind
	mu      sync.Mutex
	items   map[string]model.Item
	calls   map[string]int // fetches per item
	discuss []string       // replies and resolves, as "op thread text"
	found   map[string][]string
	reasons []string // asked of the last Discover
}

func (f *fakePR) Discover(_ context.Context, _ *github.Client, reasons []string) (map[string][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reasons = reasons
	return f.found, nil
}

func (f *fakePR) Reply(_ context.Context, _ *github.Client, it model.Item, thread, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discuss = append(f.discuss, "reply "+thread+" "+body)
	return nil
}

func (f *fakePR) Resolve(_ context.Context, _ *github.Client, it model.Item, thread string, resolved bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discuss = append(f.discuss, fmt.Sprintf("resolve %s %v", thread, resolved))
	return nil
}

func (f *fakePR) set(it model.Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it.Kind = pr.Name
	it.State = model.ItemState(it)
	f.items[it.ID] = it
}

func (f *fakePR) Fetch(_ context.Context, _ *github.Client, ids []string) ([]model.Item, *github.RateLimit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Item
	for _, id := range ids {
		if f.calls == nil {
			f.calls = map[string]int{}
		}
		f.calls[id]++
		it, ok := f.items[id]
		if !ok {
			it = model.Item{ID: id, Error: "not found"}
		}
		out = append(out, it)
	}
	return out, nil, nil
}

type recorder struct {
	mu    sync.Mutex
	notes []notify.Notification
}

func (r *recorder) Notify(_ context.Context, n notify.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, n)
	return nil
}

// settle is how long to wait before checking that nothing was sent.
const settle = 100 * time.Millisecond

// wait returns the notifications once there are at least n of them.
// Notifications are sent just after the state is broadcast, so a client
// can see the new state a moment before they are recorded.
func (r *recorder) wait(t *testing.T, n int) []notify.Notification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		notes := r.all()
		if len(notes) >= n || time.Now().After(deadline) {
			return notes
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *recorder) all() []notify.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Notification(nil), r.notes...)
}

func testPaths(t *testing.T) config.Paths {
	// Unix socket paths are limited to ~100 bytes: keep the runtime dir short.
	rt, err := os.MkdirTemp("", "gw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(rt) })
	dir := t.TempDir()
	return config.Paths{ConfigDir: filepath.Join(dir, "config"), StateDir: filepath.Join(dir, "state"), RuntimeDir: rt}
}

func check(name string, s model.State) model.Check {
	return model.Check{Name: name, Source: "Prow", State: s, URL: "https://prow/" + name}
}

func startDaemon(t *testing.T, paths config.Paths, k kind.Kind, n notify.Notifier) (*Daemon, context.CancelFunc, chan error) {
	t.Helper()
	return startDaemonWith(t, paths, config.Config{Interval: time.Hour}, k, n)
}

func startDaemonWith(t *testing.T, paths config.Paths, cfg config.Config, k kind.Kind, n notify.Notifier) (*Daemon, context.CancelFunc, chan error) {
	t.Helper()
	d := &Daemon{
		Paths:      paths,
		Config:     cfg,
		Kinds:      kind.NewRegistry(k),
		Notifier:   n,
		Log:        log.New(io.Discard, "", 0),
		WatchEvery: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := ipc.Dial(paths.Socket()); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return d, cancel, done
}

// waitFor reads snapshots from c until ok accepts one.
func waitFor(t *testing.T, c *ipc.Client, what string, ok func(*model.Snapshot) bool) *model.Snapshot {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case s, open := <-c.Snapshots():
			if !open {
				t.Fatalf("waiting for %s: connection closed", what)
			}
			if ok(s) {
				return s
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func dial(t *testing.T, paths config.Paths) *ipc.Client {
	t.Helper()
	c, err := ipc.Dial(paths.Socket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestMultiClient(t *testing.T) {
	paths := testPaths(t)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, Title: "Fix", HeadSHA: "a", Lifecycle: model.Open,
		Checks: []model.Check{check("unit", model.Running), check("e2e", model.Running)}})
	fake.set(model.Item{ID: "pr:o/r#2", Repo: "o/r", Number: 2, Title: "Docs", HeadSHA: "b", Lifecycle: model.Open,
		Checks: []model.Check{check("lint", model.Passed)}})
	rec := &recorder{}
	d, cancel, done := startDaemon(t, paths, fake, rec)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}()

	c1, c2 := dial(t, paths), dial(t, paths)
	polled := func(s *model.Snapshot) bool {
		return len(s.Items) == 1 && s.Items[0].HeadSHA == "a" && !s.PolledAt.IsZero()
	}
	waitFor(t, c1, "first poll", polled)
	waitFor(t, c2, "first poll", polled)
	c3 := dial(t, paths)
	waitFor(t, c3, "state on connect", polled)
	if n := d.Fetches(); n != 1 {
		t.Errorf("fetches = %d with 3 clients, want 1", n)
	}

	// A second daemon refuses to start.
	d2 := &Daemon{Paths: paths, Config: config.Config{Interval: time.Hour}, Kinds: kind.NewRegistry(fake), Log: log.New(io.Discard, "", 0)}
	if err := d2.Run(context.Background()); !errors.Is(err, ErrRunning) {
		t.Errorf("second daemon: %v, want ErrRunning", err)
	}

	// Notifications are off by default: a failure sends nothing.
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open,
		Checks: []model.Check{check("unit", model.Failed), check("e2e", model.Running)}})
	if _, err := c1.Do(context.Background(), ipc.Command{Op: ipc.OpPoll}); err != nil {
		t.Fatal(err)
	}
	failed := func(s *model.Snapshot) bool { return len(s.Items) == 1 && s.Items[0].State == model.Failed }
	waitFor(t, c1, "failure", failed)
	waitFor(t, c2, "failure", failed)
	time.Sleep(settle) // let a wrongly sent notification arrive
	if n := len(rec.all()); n != 0 {
		t.Errorf("sent %d notifications with alerts off", n)
	}

	// Alerts on in one client show up in the other.
	on := true
	if _, err := c1.Do(context.Background(), ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &on}); err != nil {
		t.Fatal(err)
	}
	alerts := func(s *model.Snapshot) bool { return len(s.Items) == 1 && s.Items[0].Alerts }
	waitFor(t, c2, "alerts on", alerts)
	waitFor(t, c1, "alerts on", alerts)

	// One failing job, three clients: exactly one notification.
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open,
		Checks: []model.Check{check("unit", model.Failed), check("e2e", model.Failed)}})
	c2.Do(context.Background(), ipc.Command{Op: ipc.OpPoll})
	e2eFailed := func(s *model.Snapshot) bool { return len(s.Items) == 1 && s.Items[0].Checks[1].State == model.Failed }
	waitFor(t, c1, "e2e failure", e2eFailed)
	waitFor(t, c3, "e2e failure", e2eFailed)
	notes := rec.wait(t, 1)
	if len(notes) != 1 || notes[0].Event.Type != model.EventCheckFailed || notes[0].URL != "https://prow/e2e" {
		t.Errorf("notifications = %+v, want one e2e failure", notes)
	}

	// Disabled event types and global mute are honored.
	c1.Do(context.Background(), ipc.Command{Op: ipc.OpEvents, Events: map[model.EventType]bool{model.EventRestarted: false}})
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "new", Lifecycle: model.Open,
		Checks: []model.Check{check("unit", model.Pending)}})
	c1.Do(context.Background(), ipc.Command{Op: ipc.OpPoll})
	waitFor(t, c1, "new head", func(s *model.Snapshot) bool {
		return s.Items[0].HeadSHA == "new" && !s.Settings.Events[model.EventRestarted]
	})
	time.Sleep(settle) // let a wrongly sent notification arrive
	if n := len(rec.all()); n != 1 {
		t.Errorf("disabled event type notified: %+v", rec.all())
	}

	// Add from a client appears everywhere, unwatch removes it everywhere.
	if _, err := c3.Do(context.Background(), ipc.Command{Op: ipc.OpAdd, ID: "https://github.com/o/r/pull/2"}); err != nil {
		t.Fatal(err)
	}
	two := func(s *model.Snapshot) bool {
		return len(s.Items) == 2 && s.Items[1].Title == "Docs" && !s.Items[1].Alerts
	}
	waitFor(t, c1, "added item", two)
	waitFor(t, c2, "added item", two)
	if _, err := c3.Do(context.Background(), ipc.Command{Op: ipc.OpAdd, ID: "o/r#404"}); err == nil {
		t.Error("adding a missing PR succeeded")
	}
	if _, err := c2.Do(context.Background(), ipc.Command{Op: ipc.OpUnwatch, ID: "pr:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	only2 := func(s *model.Snapshot) bool { return len(s.Items) == 1 && s.Items[0].ID == "pr:o/r#2" }
	waitFor(t, c1, "unwatched item", only2)
	data, _ := os.ReadFile(paths.Watchlist())
	if strings.Contains(string(data), "#1") {
		t.Errorf("watchlist still lists #1:\n%s", data)
	}

	// A hand edit of the watchlist is picked up.
	os.WriteFile(paths.Watchlist(), []byte("# edited\no/r#1\npr:o/r#2\n"), 0o600)
	waitFor(t, c2, "reloaded watchlist", func(s *model.Snapshot) bool { return len(s.Items) == 2 && s.Items[0].ID == "pr:o/r#1" })

	// The snapshot file mirrors the state, with settings.
	snap, err := ReadSnapshot(paths.Snapshot())
	if err != nil || snap.Schema != model.SchemaVersion || len(snap.Items) != 2 || snap.Settings.Events[model.EventRestarted] {
		t.Errorf("snapshot file = %+v, %v", snap, err)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open, Checks: []model.Check{check("unit", model.Running)}})

	_, cancel, done := startDaemon(t, paths, fake, notify.Nop{})
	c := dial(t, paths)
	waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "a" })
	on := true
	c.Do(context.Background(), ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &on})
	c.Do(context.Background(), ipc.Command{Op: ipc.OpMute, On: &on})
	waitFor(t, c, "settings", func(s *model.Snapshot) bool { return s.Items[0].Alerts && s.Settings.Mute })
	cancel()
	<-done
	select { // the client notices the daemon going away
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client not disconnected")
	}

	_, cancel, done = startDaemon(t, paths, fake, notify.Nop{})
	defer func() { cancel(); <-done }()
	c = dial(t, paths)
	waitFor(t, c, "restored settings", func(s *model.Snapshot) bool { return s.Items[0].Alerts && s.Settings.Mute })
}

func TestBackoff(t *testing.T) {
	if got := backoff(time.Minute, 1); got != time.Minute {
		t.Errorf("1 failure: %s", got)
	}
	if got := backoff(time.Minute, 3); got != 4*time.Minute {
		t.Errorf("3 failures: %s", got)
	}
	if got := backoff(time.Minute, 30); got != 15*time.Minute {
		t.Errorf("30 failures: %s", got)
	}
}

func TestJobAlerts(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	set := func(sha string, unit, e2e model.State) {
		fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: sha, Lifecycle: model.Open,
			Checks: []model.Check{check("unit", unit), check("e2e", e2e)}})
	}
	set("a", model.Running, model.Running)
	rec := &recorder{}
	_, cancel, done := startDaemon(t, paths, fake, rec)
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "a" })

	ctx := context.Background()
	on := true
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpCheckAlerts, ID: "pr:o/r#1", Check: "e2e", On: &on}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "watched job", func(s *model.Snapshot) bool { return s.Items[0].Watching("e2e") && !s.Items[0].Alerts })

	poll := func(what string, ok func(*model.Snapshot) bool) {
		t.Helper()
		if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpPoll}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, c, what, ok)
	}
	// The PR has alerts off: an unwatched job failing is silent, the
	// watched one passing notifies.
	set("a", model.Failed, model.Passed)
	poll("results", func(s *model.Snapshot) bool { return s.Items[0].Checks[1].State == model.Passed })
	notes := rec.wait(t, 1)
	if len(notes) != 1 || notes[0].Title != "√ e2e passed" || notes[0].URL != "https://prow/e2e" {
		t.Fatalf("notifications = %+v, want one for e2e passing", notes)
	}

	// The job stays watched across a new push, and with PR alerts on too,
	// its failure still sends a single notification.
	c.Do(ctx, ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &on})
	set("b", model.Running, model.Running)
	poll("new head", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "b" && s.Items[0].Alerts })
	set("b", model.Running, model.Failed)
	poll("e2e failure", func(s *model.Snapshot) bool { return s.Items[0].Checks[1].State == model.Failed })
	// e2e passing, the restart, then the failure; a duplicate would come
	// right after, so let it arrive.
	rec.wait(t, 3)
	time.Sleep(settle)
	notes = rec.all()
	var e2eFailed int
	for _, n := range notes {
		if n.Event.Type == model.EventCheckFailed && n.Event.Check == "e2e" {
			e2eFailed++
		}
	}
	if e2eFailed != 1 {
		t.Errorf("e2e failure notified %d times: %+v", e2eFailed, notes)
	}

	// Global mute silences watched jobs too; turning the bell off removes it.
	c.Do(ctx, ipc.Command{Op: ipc.OpMute, On: &on})
	before := len(rec.all())
	set("b", model.Running, model.Running)
	poll("rerun", func(s *model.Snapshot) bool { return s.Items[0].Checks[1].State == model.Running && s.Settings.Mute })
	time.Sleep(settle) // let a wrongly sent notification arrive
	if len(rec.all()) != before {
		t.Errorf("muted, but notified: %+v", rec.all()[before:])
	}
	off := false
	c.Do(ctx, ipc.Command{Op: ipc.OpCheckAlerts, ID: "pr:o/r#1", Check: "e2e", On: &off})
	waitFor(t, c, "bell off", func(s *model.Snapshot) bool { return len(s.Items[0].WatchedChecks) == 0 })
}

func TestMuteItem(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	set := func(unit, e2e model.State) {
		fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open,
			Checks: []model.Check{check("unit", unit), check("e2e", e2e)}})
	}
	set(model.Running, model.Running)
	rec := &recorder{}
	_, cancel, done := startDaemon(t, paths, fake, rec)
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "a" })
	ctx := context.Background()
	on, off := true, false
	poll := func(what string, ok func(*model.Snapshot) bool) {
		t.Helper()
		if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpPoll}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, c, what, ok)
	}

	// Nothing to mute yet.
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpMuteItem, ID: "pr:o/r#1", On: &on}); err == nil {
		t.Error("muting a PR without alerts should fail")
	}

	// Muting keeps the alerts and the bells, and silences them.
	c.Do(ctx, ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &on})
	c.Do(ctx, ipc.Command{Op: ipc.OpCheckAlerts, ID: "pr:o/r#1", Check: "e2e", On: &on})
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpMuteItem, ID: "pr:o/r#1", On: &on}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "muted", func(s *model.Snapshot) bool {
		it := s.Items[0]
		return it.Muted && it.Alerts && it.Watching("e2e")
	})
	set(model.Failed, model.Passed)
	poll("results", func(s *model.Snapshot) bool { return s.Items[0].Checks[1].State == model.Passed })
	time.Sleep(settle)
	if n := rec.all(); len(n) != 0 {
		t.Errorf("muted, but notified: %+v", n)
	}

	// Unmuting restores them.
	c.Do(ctx, ipc.Command{Op: ipc.OpMuteItem, ID: "pr:o/r#1", On: &off})
	waitFor(t, c, "unmuted", func(s *model.Snapshot) bool { return !s.Items[0].Muted && s.Items[0].Alerts })
	set(model.Failed, model.Failed)
	poll("e2e failure", func(s *model.Snapshot) bool { return s.Items[0].Checks[1].State == model.Failed })
	if n := rec.wait(t, 1); len(n) != 1 || n[0].Event.Check != "e2e" {
		t.Errorf("unmuted, notifications = %+v", n)
	}

	// Ringing a bell while muted unmutes, so the new bell works.
	c.Do(ctx, ipc.Command{Op: ipc.OpMuteItem, ID: "pr:o/r#1", On: &on})
	waitFor(t, c, "muted again", func(s *model.Snapshot) bool { return s.Items[0].Muted })
	c.Do(ctx, ipc.Command{Op: ipc.OpCheckAlerts, ID: "pr:o/r#1", Check: "unit", On: &on})
	waitFor(t, c, "bell unmutes", func(s *model.Snapshot) bool { return !s.Items[0].Muted && s.Items[0].Watching("unit") })

	// Clearing removes everything, muted or not.
	c.Do(ctx, ipc.Command{Op: ipc.OpMuteItem, ID: "pr:o/r#1", On: &on})
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpClearAlerts, ID: "pr:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "cleared", func(s *model.Snapshot) bool {
		it := s.Items[0]
		return !it.Alerts && it.WatchedChecks == nil && !it.Muted
	})
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpClearAlerts, ID: "pr:o/r#9"}); err == nil {
		t.Error("clearing an unwatched PR should fail")
	}
}

func TestIdleExit(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	fake := &fakePR{items: map[string]model.Item{}}
	d := &Daemon{
		Paths: paths, Config: config.Config{Interval: time.Hour}, Kinds: kind.NewRegistry(fake),
		Log: log.New(io.Discard, "", 0), WatchEvery: 10 * time.Millisecond, IdleExit: 300 * time.Millisecond,
	}
	done := make(chan error, 1)
	go func() { done <- d.Run(context.Background()) }()
	var c *ipc.Client
	for deadline := time.Now().Add(5 * time.Second); c == nil; {
		c, _ = ipc.Dial(paths.Socket())
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
	}

	// A connected client keeps it alive well past the idle time.
	select {
	case err := <-done:
		t.Fatalf("exited with a client connected: %v", err)
	case <-time.After(3 * d.IdleExit):
	}

	// The last client leaving starts the countdown.
	c.Close()
	start := time.Now()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if waited := time.Since(start); waited < d.IdleExit-50*time.Millisecond {
			t.Errorf("exited %s after the last client, before the %s grace period", waited, d.IdleExit)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit without clients")
	}
	if Running(paths.Lock()) {
		t.Error("lock still held")
	}
	if _, err := os.Stat(paths.Socket()); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
}

func TestFinishedItemRefreshedOnStart(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	// Saved by an older build: merged, but without labels.
	old := model.Item{Kind: "pr", ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.LifeMerged, State: model.Merged}
	if err := WriteSnapshot(paths.Snapshot(), &model.Snapshot{Schema: 1, Items: []model.Item{old}, Settings: model.DefaultSettings()}); err != nil {
		t.Fatal(err)
	}
	fake := &fakePR{items: map[string]model.Item{}}
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.LifeMerged,
		Labels: []model.Label{{Name: "lgtm", Color: "0e8a16"}}})
	_, cancel, done := startDaemon(t, paths, fake, notify.Nop{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	waitFor(t, c, "labels fetched", func(s *model.Snapshot) bool { return len(s.Items[0].Labels) == 1 })

	// Once fetched, the finished item is left alone.
	for i := 0; i < 2; i++ {
		c.Do(context.Background(), ipc.Command{Op: ipc.OpPoll})
		waitFor(t, c, "poll", func(*model.Snapshot) bool { return true })
	}
	fake.mu.Lock()
	n := fake.calls["pr:o/r#1"]
	fake.mu.Unlock()
	if n != 1 {
		t.Errorf("finished item fetched %d times, want 1", n)
	}
}

func TestUnseen(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	set := func(s model.State) {
		fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open,
			Checks: []model.Check{check("unit", s)}})
	}
	set(model.Running)
	_, cancel, done := startDaemon(t, paths, fake, notify.Nop{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	ctx := context.Background()
	waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "a" && !s.Items[0].Unseen })

	// A failure marks the PR, whatever its alerts.
	set(model.Failed)
	c.Do(ctx, ipc.Command{Op: ipc.OpPoll})
	waitFor(t, c, "unseen", func(s *model.Snapshot) bool { return s.Items[0].Unseen })

	// It stays marked across polls without changes, and is saved.
	c.Do(ctx, ipc.Command{Op: ipc.OpPoll})
	waitFor(t, c, "still unseen", func(s *model.Snapshot) bool { return s.Items[0].Unseen })
	if snap, _ := ReadSnapshot(paths.Snapshot()); !snap.Items[0].Unseen {
		t.Error("unseen mark not saved")
	}

	// Looking at it clears the mark for every client.
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpSeen, ID: "pr:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "seen", func(s *model.Snapshot) bool { return !s.Items[0].Unseen })
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpSeen, ID: "pr:o/r#9"}); err == nil {
		t.Error("seen on an unknown PR succeeded")
	}
}

func TestComments(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	base := time.Now().UTC() // comments after the first poll are new
	comment := func(id, author string, min int) model.Comment {
		return model.Comment{ID: id, Author: author, Body: "text of " + id, CreatedAt: base.Add(time.Duration(min) * time.Minute), URL: "u/" + id}
	}
	var conv, thread []model.Comment
	set := func() {
		fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open, URL: "https://pr",
			Checks: []model.Check{check("unit", model.Running)},
			Threads: []model.Thread{
				{ID: model.ConversationID, Total: len(conv), Comments: slices.Clone(conv)},
				{ID: "T1", Path: "a.go", Line: 3, Total: len(thread), Comments: slices.Clone(thread)},
			}})
	}
	conv = []model.Comment{comment("c1", "alice", -60)}
	set()
	rec := &recorder{}
	cfg := config.Config{Interval: time.Hour, IgnoreAuthors: []string{"ci-robot"}}
	_, cancel, done := startDaemonWith(t, paths, cfg, fake, rec)
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	ctx := context.Background()
	on := true
	poll := func(what string, ok func(*model.Snapshot) bool) {
		t.Helper()
		if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpPoll}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, c, what, ok)
	}

	// The comments there when the PR is first fetched are read.
	if s := waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return len(s.Items[0].Threads) == 2 }); s.Items[0].NewComments() != 0 || s.Items[0].CommentsReadAt.IsZero() {
		t.Fatalf("after the first poll: %d new, read at %v", s.Items[0].NewComments(), s.Items[0].CommentsReadAt)
	}

	// With PR alerts on, a new comment notifies; the user's own and an
	// ignored author's neither notify nor count.
	c.Do(ctx, ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &on})
	mine := comment("c3", "me", 3)
	mine.Mine = true
	conv = append(conv, comment("c2", "alice", 2), mine, comment("c4", "CI-Robot", 4))
	set()
	var s *model.Snapshot
	poll("new comments", func(x *model.Snapshot) bool { s = x; return len(x.Items[0].Threads[0].Comments) == 4 })
	notes := rec.wait(t, 1)
	if len(notes) != 1 || notes[0].Title != "alice commented" || notes[0].URL != "u/c2" {
		t.Fatalf("notifications = %+v", notes)
	}
	if n := s.Items[0].NewComments(); n != 1 || !s.Items[0].Threads[0].Comments[3].Ignored {
		t.Errorf("%d new comments, ignored flag %v", n, s.Items[0].Threads[0].Comments[3].Ignored)
	}

	// With PR alerts off, only a thread with a bell notifies.
	off := false
	c.Do(ctx, ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &off})
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpThreadAlerts, ID: "pr:o/r#1", Thread: "T1", On: &on}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, c, "thread bell", func(s *model.Snapshot) bool { return s.Items[0].WatchingThread("T1") && !s.Items[0].Alerts })
	conv = append(conv, comment("c5", "alice", 5))
	thread = append(thread, comment("r1", "bob", 6))
	set()
	poll("reply", func(s *model.Snapshot) bool { return len(s.Items[0].Threads[1].Comments) == 1 })
	notes = rec.wait(t, 2)
	if len(notes) != 2 || notes[1].Title != "bob commented on a.go:3" || !strings.Contains(notes[1].Body, "text of r1") {
		t.Fatalf("notifications = %+v", notes)
	}

	// Several at once make one notification.
	thread = append(thread, comment("r2", "carol", 7), comment("r3", "bob", 8))
	set()
	poll("more replies", func(x *model.Snapshot) bool { s = x; return len(x.Items[0].Threads[1].Comments) == 3 })
	notes = rec.wait(t, 3)
	if len(notes) != 3 || notes[2].Title != "2 new comments from carol, bob" || notes[2].URL != "https://pr" {
		t.Fatalf("notifications = %+v", notes)
	}

	// Reading clears the count; clearing alerts drops the thread bell.
	if n := s.Items[0].NewComments(); n != 5 {
		t.Errorf("before reading, %d new, want 5", n)
	}
	c.Do(ctx, ipc.Command{Op: ipc.OpCommentsRead, ID: "pr:o/r#1"})
	waitFor(t, c, "read", func(s *model.Snapshot) bool { return s.Items[0].NewComments() == 0 })
	c.Do(ctx, ipc.Command{Op: ipc.OpClearAlerts, ID: "pr:o/r#1"})
	waitFor(t, c, "cleared", func(s *model.Snapshot) bool { return s.Items[0].WatchedThreads == nil })
}

func TestDiscuss(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}}
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open,
		Threads: []model.Thread{{ID: model.ConversationID}, {ID: "T1", Path: "a.go", Line: 3}}})
	_, cancel, done := startDaemon(t, paths, fake, &recorder{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return len(s.Items[0].Threads) == 2 })
	ctx := context.Background()
	on, off := true, false
	fake.mu.Lock()
	before := fake.calls["pr:o/r#1"]
	fake.mu.Unlock()
	for _, tc := range []struct {
		cmd  ipc.Command
		info string
	}{
		{ipc.Command{Op: ipc.OpReply, ID: "pr:o/r#1", Thread: model.ConversationID, Body: "LGTM"}, "commented on o/r#1"},
		{ipc.Command{Op: ipc.OpReply, ID: "pr:o/r#1", Thread: "T1", Body: "done"}, "replied on a.go:3"},
		{ipc.Command{Op: ipc.OpResolve, ID: "pr:o/r#1", Thread: "T1", On: &on}, "resolved a.go:3"},
		{ipc.Command{Op: ipc.OpResolve, ID: "pr:o/r#1", Thread: "T1", On: &off}, "unresolved a.go:3"},
	} {
		if info, err := c.Do(ctx, tc.cmd); err != nil || info != tc.info {
			t.Errorf("%+v: %q, %v", tc.cmd, info, err)
		}
	}
	if got := strings.Join(fake.discuss, "|"); got != "reply conversation LGTM|reply T1 done|resolve T1 true|resolve T1 false" {
		t.Errorf("calls = %s", got)
	}
	for _, bad := range []ipc.Command{
		{Op: ipc.OpReply, ID: "pr:o/r#1", Thread: "T1", Body: "  "},
		{Op: ipc.OpResolve, ID: "pr:o/r#1", Thread: "T1"},
		{Op: ipc.OpReply, ID: "pr:o/r#9", Thread: "T1", Body: "x"},
	} {
		if _, err := c.Do(ctx, bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// Each change asked for a poll, so it shows.
	deadline := time.Now().Add(5 * time.Second)
	for {
		fake.mu.Lock()
		n := fake.calls["pr:o/r#1"]
		fake.mu.Unlock()
		if n > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no poll after replying")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAutoAdd(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}, found: map[string][]string{
		"pr:o/r#2": {model.AddedReview},
		"pr:o/r#3": {model.AddedReview, model.AddedAssigned},
		"pr:o/r#4": {model.AddedAssigned},
		"pr:o/r#1": {model.AddedReview}, // watched already, by hand
	}}
	for n := 1; n <= 4; n++ {
		fake.set(model.Item{ID: fmt.Sprintf("pr:o/r#%d", n), Repo: "o/r", Number: n, HeadSHA: "a", Lifecycle: model.Open})
	}
	cfg := config.Config{Interval: time.Hour, AutoAddReviews: true, AutoAddAssigned: true, AutoAddMax: 2}
	_, cancel, done := startDaemonWith(t, paths, cfg, fake, &recorder{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	ctx := context.Background()
	ids := func(s *model.Snapshot) string {
		var out []string
		for _, it := range s.Items {
			out = append(out, fmt.Sprintf("%d%v", it.Number, it.Added))
		}
		return strings.Join(out, " ")
	}

	// The first poll adds #2 and #3, up to the cap of 2, with their
	// reasons, as unseen; #1 stays a PR added by hand.
	s := waitFor(t, c, "auto-added", func(s *model.Snapshot) bool { return len(s.Items) == 3 && s.Items[2].HeadSHA != "" })
	if got := ids(s); got != "1[] 2[review requested] 3[review requested assigned]" {
		t.Fatalf("items = %s", got)
	}
	if !s.Items[1].Unseen || s.Items[0].Unseen {
		t.Errorf("unseen: %v %v", s.Items[0].Unseen, s.Items[1].Unseen)
	}
	data, _ := os.ReadFile(paths.Watchlist())
	if !strings.Contains(string(data), "pr:o/r#3  # auto: review requested, assigned\n") {
		t.Errorf("watchlist =\n%s", data)
	}
	fake.mu.Lock()
	if strings.Join(fake.reasons, ",") != "review requested,assigned" {
		t.Errorf("searched for %v", fake.reasons)
	}
	fake.mu.Unlock()

	// Removing #2 dismisses it: the next poll adds #4 in its place, not
	// #2 again.
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpUnwatch, ID: "pr:o/r#2"}); err != nil {
		t.Fatal(err)
	}
	c.Do(ctx, ipc.Command{Op: ipc.OpPoll})
	s = waitFor(t, c, "#4 added", func(s *model.Snapshot) bool { return len(s.Items) == 3 && s.Items[2].Number == 4 })
	if got := ids(s); got != "1[] 3[review requested assigned] 4[assigned]" || strings.Join(s.Dismissed, ",") != "pr:o/r#2" {
		t.Errorf("items = %s, dismissed %v", got, s.Dismissed)
	}

	// Adding #2 by hand lifts the dismissal.
	if _, err := c.Do(ctx, ipc.Command{Op: ipc.OpAdd, ID: "o/r#2"}); err != nil {
		t.Fatal(err)
	}
	s = waitFor(t, c, "#2 back", func(s *model.Snapshot) bool { return len(s.Items) == 4 })
	if len(s.Dismissed) != 0 || len(s.Find("pr:o/r#2").Added) != 0 {
		t.Errorf("after adding by hand: dismissed %v, added %v", s.Dismissed, s.Find("pr:o/r#2").Added)
	}
}

func TestAutoAddOff(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}, found: map[string][]string{"pr:o/r#2": {model.AddedReview}}}
	fake.set(model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, HeadSHA: "a", Lifecycle: model.Open})
	_, cancel, done := startDaemon(t, paths, fake, &recorder{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	waitFor(t, c, "first poll", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "a" })
	time.Sleep(settle)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.reasons != nil {
		t.Errorf("searched with auto-add off: %v", fake.reasons)
	}
}

func TestAutoRemove(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte("o/r#1\n"), 0o600)
	fake := &fakePR{items: map[string]model.Item{}, found: map[string][]string{
		"pr:o/r#2": {model.AddedReview},
		"pr:o/r#3": {model.AddedReview},
		"pr:o/r#4": {model.AddedAssigned},
		"pr:o/r#5": {model.AddedReview, model.AddedAssigned},
	}}
	for n := 1; n <= 5; n++ {
		it := model.Item{ID: fmt.Sprintf("pr:o/r#%d", n), Repo: "o/r", Number: n, HeadSHA: "a", Lifecycle: model.Open}
		if n == 3 { // the user approved #3, as GitHub records it
			it.Review = model.MyReview{State: "approved"}
		}
		fake.set(it)
	}
	cfg := config.Config{Interval: time.Hour, AutoAddReviews: true, AutoAddAssigned: true, AutoAddMax: 10}
	_, cancel, done := startDaemonWith(t, paths, cfg, fake, &recorder{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	ctx := context.Background()
	state := func(s *model.Snapshot) string {
		var out []string
		for _, it := range s.Items {
			out = append(out, fmt.Sprintf("%d%v", it.Number, it.Added))
		}
		return strings.Join(out, " ")
	}
	s := waitFor(t, c, "all added", func(s *model.Snapshot) bool { return len(s.Items) == 5 })
	if s.Notice != "added o/r#2, o/r#3, o/r#4, o/r#5" {
		t.Errorf("notice = %q", s.Notice)
	}

	// The requests and assignments change.
	fake.mu.Lock()
	fake.found = map[string][]string{"pr:o/r#5": {model.AddedReview}}
	fake.mu.Unlock()
	c.Do(ctx, ipc.Command{Op: ipc.OpPoll})
	s = waitFor(t, c, "pruned", func(s *model.Snapshot) bool { return len(s.Items) == 3 })
	if got := state(s); got != "1[] 3[review requested] 5[review requested]" {
		t.Errorf("items = %s", got)
	}
	if s.Notice != "removed o/r#2: no longer requests your review; o/r#4: no longer assigned to you" || len(s.Dismissed) != 0 {
		t.Errorf("notice = %q, dismissed %v", s.Notice, s.Dismissed)
	}
	data, _ := os.ReadFile(paths.Watchlist())
	if strings.Contains(string(data), "#2") || strings.Contains(string(data), "#4") {
		t.Errorf("watchlist =\n%s", data)
	}

	// Not dismissed: requested again, #2 comes back.
	fake.mu.Lock()
	fake.found["pr:o/r#2"] = []string{model.AddedReview}
	fake.mu.Unlock()
	c.Do(ctx, ipc.Command{Op: ipc.OpPoll})
	if s := waitFor(t, c, "#2 back", func(s *model.Snapshot) bool { return len(s.Items) == 4 }); s.Find("pr:o/r#2") == nil {
		t.Errorf("items = %s", state(s))
	}
}

func TestPruneKeepsUnsearchedReasons(t *testing.T) {
	d := &Daemon{Paths: testPaths(t), Log: log.New(io.Discard, "", 0)}
	d.snap.Items = []model.Item{
		{ID: "pr:o/r#1", Added: []string{model.AddedReview}},
		{ID: "pr:o/r#2", Added: []string{model.AddedAssigned, model.AddedReview}},
	}
	// Only assignments are searched now (review_requested was turned
	// off): review requests are left as they are, assignments pruned.
	d.pruneLocked(map[string][]string{}, []string{model.AddedAssigned})
	if len(d.snap.Items) != 2 || strings.Join(d.snap.Items[0].Added, ",") != "review requested" || strings.Join(d.snap.Items[1].Added, ",") != "review requested" {
		t.Errorf("items = %+v", d.snap.Items)
	}
}

func TestAutoAddRepos(t *testing.T) {
	paths := testPaths(t)
	paths.Ensure()
	os.WriteFile(paths.Watchlist(), []byte(""), 0o600)
	fake := &fakePR{items: map[string]model.Item{}, found: map[string][]string{
		"pr:mine/a#1":   {model.AddedReview},
		"pr:other/b#2":  {model.AddedReview},
		"pr:mine-x/c#3": {model.AddedAssigned},
	}}
	for _, id := range []string{"pr:mine/a#1", "pr:other/b#2", "pr:mine-x/c#3"} {
		it := (pr.Kind{}).Stub(id)
		it.HeadSHA = "a"
		fake.set(it)
	}
	cfg := config.Config{Interval: time.Hour, AutoAddReviews: true, AutoAddAssigned: true, AutoAddMax: 10, AutoAddRepos: []string{"mine/*"}}
	_, cancel, done := startDaemonWith(t, paths, cfg, fake, &recorder{})
	defer func() { cancel(); <-done }()
	c := dial(t, paths)
	s := waitFor(t, c, "added", func(s *model.Snapshot) bool { return len(s.Items) > 0 && s.Items[0].HeadSHA != "" })
	time.Sleep(settle)
	if len(s.Items) != 1 || s.Items[0].ID != "pr:mine/a#1" {
		t.Errorf("items = %+v", s.Items)
	}
}

func TestReviewedByUser(t *testing.T) {
	for _, tc := range []struct {
		it   model.Item
		want bool
	}{
		{model.Item{}, false},
		{model.Item{Review: model.MyReview{State: "commented"}}, true},
		{model.Item{Review: model.MyReview{State: "pending"}}, false}, // a draft is not a review yet
		{model.Item{Threads: []model.Thread{{ID: model.ConversationID, Comments: []model.Comment{{Review: "approved", Mine: true}}}}}, true},
		{model.Item{Threads: []model.Thread{{ID: model.ConversationID, Comments: []model.Comment{{Mine: true}, {Review: "approved"}}}}}, false},
	} {
		if got := reviewedByUser(tc.it); got != tc.want {
			t.Errorf("%+v: %v", tc.it, got)
		}
	}
}
