package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
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
	mu    sync.Mutex
	items map[string]model.Item
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
	d := &Daemon{
		Paths:      paths,
		Config:     config.Config{Interval: time.Hour},
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
	if len(notes) != 1 || notes[0].Title != "✓ e2e passed" || notes[0].URL != "https://prow/e2e" {
		t.Fatalf("notifications = %+v, want one for e2e passing", notes)
	}

	// The job stays watched across a new push, and with PR alerts on too,
	// its failure still sends a single notification.
	c.Do(ctx, ipc.Command{Op: ipc.OpAlerts, ID: "pr:o/r#1", On: &on})
	set("b", model.Running, model.Running)
	poll("new head", func(s *model.Snapshot) bool { return s.Items[0].HeadSHA == "b" && s.Items[0].Alerts })
	set("b", model.Running, model.Failed)
	poll("e2e failure", func(s *model.Snapshot) bool { return s.Items[0].Checks[1].State == model.Failed })
	notes = rec.wait(t, 2)
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
