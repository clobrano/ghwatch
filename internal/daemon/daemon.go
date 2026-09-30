// Package daemon owns all polling and state: it reads the watchlist,
// fetches every item in one batched request per cycle, diffs checks
// against the last known state, sends notifications, persists the
// snapshot and broadcasts it to clients.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/kind"
	"github.com/clobrano/ghwatch/internal/model"
	"github.com/clobrano/ghwatch/internal/notify"
	"github.com/clobrano/ghwatch/internal/watchlist"
)

// Daemon is the single per-user ghwatch server.
type Daemon struct {
	Paths    config.Paths
	Config   config.Config
	Kinds    *kind.Registry
	GH       *github.Client
	Notifier notify.Notifier
	Log      *log.Logger
	// Now is the clock, replaceable in tests.
	Now func() time.Time
	// WatchEvery is how often the watchlist file is checked for changes.
	WatchEvery time.Duration
	// IdleExit makes the daemon exit once it has had no client for this
	// long; 0 keeps it running. Auto-started daemons use AutoIdleExit, so
	// an old daemon goes away with the last TUI (for example after a rebuild).
	IdleExit time.Duration

	mu        sync.Mutex
	snap      model.Snapshot
	watchStat fileStat
	server    *ipc.Server
	pollNow   chan struct{}
	fetches   atomic.Int64
	// lastClient is when a client was last seen connected (loop only).
	lastClient time.Time
}

// AutoIdleExit is the IdleExit of daemons started by clients.
const AutoIdleExit = 10 * time.Second

type fileStat struct {
	mod  time.Time
	size int64
	ok   bool
}

// Fetches returns the number of fetch requests made so far, one per kind
// per poll cycle (the API budget does not depend on the number of clients).
func (d *Daemon) Fetches() int64 { return d.fetches.Load() }

func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d *Daemon) parse(s string) (string, error) {
	_, id, err := d.Kinds.Parse(s)
	return id, err
}

// Run starts the daemon and blocks until ctx is done. It returns
// ErrRunning if another daemon is already running for this user.
func (d *Daemon) Run(ctx context.Context) error {
	if d.Log == nil {
		d.Log = log.New(os.Stderr, "ghwatch: ", log.LstdFlags)
	}
	if d.Notifier == nil {
		d.Notifier = notify.Nop{}
	}
	if d.WatchEvery == 0 {
		d.WatchEvery = 2 * time.Second
	}
	if err := d.Paths.Ensure(); err != nil {
		return err
	}
	lock, err := AcquireLock(d.Paths.Lock())
	if err != nil {
		return err
	}
	defer lock.Release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	d.pollNow = make(chan struct{}, 1)
	d.loadState()
	if _, err := d.reloadWatchlist(true); err != nil {
		d.Log.Printf("watchlist: %v", err)
	}
	d.mu.Lock()
	d.persistLocked()
	d.mu.Unlock()

	srv, err := ipc.Listen(d.Paths.Socket(), d)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.mu.Lock()
	d.server = srv
	d.mu.Unlock()
	defer os.Remove(d.Paths.Socket())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()
	d.Log.Printf("serving on %s, polling every %s", d.Paths.Socket(), d.Config.Interval)

	d.loop(ctx)
	cancel()
	if err := <-serveErr; err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (d *Daemon) loop(ctx context.Context) {
	watch := time.NewTicker(d.WatchEvery)
	defer watch.Stop()
	next := time.NewTimer(0)
	defer next.Stop()
	failures := 0
	d.lastClient = time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-watch.C:
			if d.idle(time.Now()) {
				d.Log.Printf("no clients for %s, exiting", d.IdleExit)
				return
			}
			changed, err := d.reloadWatchlist(false)
			if err != nil {
				d.Log.Printf("watchlist: %v", err)
			}
			if !changed {
				continue
			}
		case <-d.pollNow:
		case <-next.C:
		}
		wait := d.Config.Interval
		rl, err := d.Poll(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			failures++
			wait = backoff(d.Config.Interval, failures)
			d.Log.Printf("poll failed (%d in a row, next in %s): %v", failures, wait, err)
		default:
			failures = 0
			if rl != nil && rl.Low() {
				if until := time.Until(rl.ResetAt); until > wait {
					wait = until
					d.Log.Printf("rate limit low (%d/%d left), waiting until %s", rl.Remaining, rl.Limit, rl.ResetAt.Local().Format(time.Kitchen))
				}
			}
		}
		if !next.Stop() {
			select {
			case <-next.C:
			default:
			}
		}
		next.Reset(wait)
	}
}

// idle reports whether the daemon should exit for lack of clients.
func (d *Daemon) idle(now time.Time) bool {
	if d.IdleExit <= 0 {
		return false
	}
	d.mu.Lock()
	srv := d.server
	d.mu.Unlock()
	if srv != nil && srv.Clients() > 0 {
		d.lastClient = now
		return false
	}
	return now.Sub(d.lastClient) >= d.IdleExit
}

// backoff doubles the interval per consecutive failure, up to 15 minutes.
func backoff(interval time.Duration, failures int) time.Duration {
	d := interval
	for i := 1; i < failures && d < 15*time.Minute; i++ {
		d *= 2
	}
	return min(d, 15*time.Minute)
}

// Poll fetches every watched item once and applies the result.
func (d *Daemon) Poll(ctx context.Context) (*github.RateLimit, error) {
	d.mu.Lock()
	byKind := map[string][]string{}
	var order []string
	for _, it := range d.snap.Items {
		// A finished item never changes again: stop spending API on it.
		if it.Lifecycle.Finished() && it.HeadSHA != "" && it.Error == "" {
			continue
		}
		k := d.Kinds.Of(it.ID)
		if k == nil {
			continue
		}
		if _, ok := byKind[k.Name()]; !ok {
			order = append(order, k.Name())
		}
		byKind[k.Name()] = append(byKind[k.Name()], it.ID)
	}
	d.mu.Unlock()

	var (
		fetched []model.Item
		rl      *github.RateLimit
		errs    []error
	)
	for _, name := range order {
		d.fetches.Add(1)
		items, r, err := d.Kinds.Get(name).Fetch(ctx, d.GH, byKind[name])
		fetched = append(fetched, items...)
		if r != nil {
			rl = r
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	err := errors.Join(errs...)

	d.mu.Lock()
	var notes []notify.Notification
	for _, it := range fetched {
		notes = append(notes, d.applyLocked(it)...)
	}
	d.snap.Stale = err != nil
	d.snap.Error = ""
	if err != nil {
		d.snap.Error = err.Error()
	} else {
		d.snap.PolledAt = d.now()
	}
	d.publishLocked()
	d.mu.Unlock()

	for _, n := range notes {
		if nerr := d.Notifier.Notify(ctx, n); nerr != nil {
			d.Log.Printf("notify: %v", nerr)
		}
	}
	return rl, err
}

// applyLocked merges a freshly fetched item into the state and returns
// the notifications it causes.
func (d *Daemon) applyLocked(cur model.Item) []notify.Notification {
	idx := -1
	for i := range d.snap.Items {
		if d.snap.Items[i].ID == cur.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil // unwatched while fetching
	}
	prev := d.snap.Items[idx]
	now := d.now()
	if cur.Error != "" {
		prev.Error = cur.Error
		d.snap.Items[idx] = prev
		return nil
	}
	cur.Alerts = prev.Alerts
	cur.WatchedChecks = prev.WatchedChecks
	var prevp *model.Item
	if prev.HeadSHA != "" {
		prevp = &prev
	}
	cur.Checks = model.MergeOrder(prevp, cur)
	if prevp != nil && prev.HeadSHA == cur.HeadSHA {
		was := map[string]model.State{}
		for _, c := range prev.Checks {
			was[c.Key()] = c.State
		}
		for i := range cur.Checks {
			c := &cur.Checks[i]
			if c.State.Done() && c.CompletedAt == nil {
				if s, ok := was[c.Key()]; ok && !s.Done() {
					c.CompletedAt = &now
				}
			}
			if c.State == model.Running && c.StartedAt == nil {
				c.StartedAt = &now
			}
		}
	}
	cur.State = model.ItemState(cur)
	cur.UpdatedAt = now
	d.snap.Items[idx] = cur

	if d.snap.Settings.Mute || !cur.Alerts && len(cur.WatchedChecks) == 0 {
		return nil
	}
	var notes []notify.Notification
	for _, t := range model.Diff(prevp, cur) {
		// One notification per event, whether it is wanted for the item,
		// for the watched check, or both.
		forItem := cur.Alerts && d.snap.Settings.Events[t.Type]
		forCheck := t.Check != "" && cur.Watching(t.Check)
		if forItem || forCheck {
			notes = append(notes, notify.Format(t, cur))
		}
	}
	return notes
}

// Snapshot implements ipc.Handler.
func (d *Daemon) Snapshot() *model.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.copyLocked()
}

func (d *Daemon) copyLocked() *model.Snapshot {
	s := d.snap
	s.Items = append([]model.Item(nil), d.snap.Items...)
	s.Settings.Events = map[model.EventType]bool{}
	for k, v := range d.snap.Settings.Events {
		s.Settings.Events[k] = v
	}
	return &s
}

// publishLocked stamps, persists and broadcasts the state.
func (d *Daemon) publishLocked() {
	d.snap.GeneratedAt = d.now()
	d.persistLocked()
	if d.server != nil {
		d.server.Broadcast(d.copyLocked())
	}
}

func (d *Daemon) persistLocked() {
	d.snap.Schema = model.SchemaVersion
	if err := WriteSnapshot(d.Paths.Snapshot(), &d.snap); err != nil {
		d.Log.Printf("write snapshot: %v", err)
	}
}

// WriteSnapshot writes snap atomically with mode 0600.
func WriteSnapshot(path string, snap *model.Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadSnapshot reads a snapshot file.
func ReadSnapshot(path string) (*model.Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s model.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (d *Daemon) loadState() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.snap = model.Snapshot{Schema: model.SchemaVersion, Settings: model.DefaultSettings()}
	s, err := ReadSnapshot(d.Paths.Snapshot())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.Log.Printf("ignoring saved state: %v", err)
		}
		return
	}
	if s.Schema > model.SchemaVersion {
		d.Log.Printf("ignoring saved state from a newer ghwatch (schema %d)", s.Schema)
		return
	}
	d.snap.Items = s.Items
	d.snap.PolledAt = s.PolledAt
	d.snap.Settings.Mute = s.Settings.Mute
	for k, v := range s.Settings.Events {
		d.snap.Settings.Events[k] = v
	}
}

// reloadWatchlist re-reads the watchlist when it changed (or always when
// force is set) and reshapes the item list to match it: order follows the
// file, removed items are dropped, new items get a stub until the next poll.
func (d *Daemon) reloadWatchlist(force bool) (bool, error) {
	path := d.Paths.Watchlist()
	st := fileStat{}
	if fi, err := os.Stat(path); err == nil {
		st = fileStat{mod: fi.ModTime(), size: fi.Size(), ok: true}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !force && st == d.watchStat {
		return false, nil
	}
	d.watchStat = st
	ids, bad, err := watchlist.Load(path, d.parse)
	if err != nil {
		return false, err
	}
	for _, e := range bad {
		d.Log.Print(e)
	}
	d.setIDsLocked(ids)
	if !force {
		d.publishLocked()
	}
	return true, nil
}

func (d *Daemon) setIDsLocked(ids []string) {
	old := map[string]model.Item{}
	for _, it := range d.snap.Items {
		old[it.ID] = it
	}
	items := make([]model.Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := old[id]; ok {
			items = append(items, it)
		} else if k := d.Kinds.Of(id); k != nil {
			items = append(items, k.Stub(id))
		}
	}
	d.snap.Items = items
}

// Handle implements ipc.Handler.
func (d *Daemon) Handle(ctx context.Context, cmd ipc.Command) (string, error) {
	switch cmd.Op {
	case ipc.OpAdd:
		return d.add(ctx, cmd.ID)
	case ipc.OpUnwatch:
		return d.unwatch(cmd.ID)
	case ipc.OpPoll:
		d.RequestPoll()
		return "polling", nil
	case ipc.OpAlerts:
		if cmd.On == nil {
			return "", errors.New("alerts: missing on")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for i := range d.snap.Items {
			if d.snap.Items[i].ID == cmd.ID {
				d.snap.Items[i].Alerts = *cmd.On
				d.publishLocked()
				return fmt.Sprintf("alerts %s for %s", onOff(*cmd.On), cmd.ID), nil
			}
		}
		return "", fmt.Errorf("not watching %s", cmd.ID)
	case ipc.OpCheckAlerts:
		if cmd.On == nil || cmd.Check == "" {
			return "", errors.New("check_alerts: missing check or on")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for i := range d.snap.Items {
			it := &d.snap.Items[i]
			if it.ID != cmd.ID {
				continue
			}
			watched := slices.DeleteFunc(slices.Clone(it.WatchedChecks), func(n string) bool { return n == cmd.Check })
			if *cmd.On {
				watched = append(watched, cmd.Check)
			}
			if len(watched) == 0 {
				watched = nil
			}
			it.WatchedChecks = watched
			d.publishLocked()
			return fmt.Sprintf("alerts %s for job %s", onOff(*cmd.On), cmd.Check), nil
		}
		return "", fmt.Errorf("not watching %s", cmd.ID)
	case ipc.OpEvents:
		for k := range cmd.Events {
			if !slices.Contains(model.EventTypes, k) {
				return "", fmt.Errorf("unknown event type %q", k)
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for k, v := range cmd.Events {
			d.snap.Settings.Events[k] = v
		}
		d.publishLocked()
		return "event types updated", nil
	case ipc.OpMute:
		if cmd.On == nil {
			return "", errors.New("mute: missing on")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.snap.Settings.Mute = *cmd.On
		d.publishLocked()
		return "mute " + onOff(*cmd.On), nil
	case ipc.OpRetest:
		return d.retest(ctx, cmd.ID)
	}
	return "", fmt.Errorf("unknown command %q", cmd.Op)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// RequestPoll asks the loop to poll as soon as possible.
func (d *Daemon) RequestPoll() {
	select {
	case d.pollNow <- struct{}{}:
	default:
	}
}

func (d *Daemon) add(ctx context.Context, input string) (string, error) {
	k, id, err := d.Kinds.Parse(input)
	if err != nil {
		return "", err
	}
	d.fetches.Add(1)
	items, _, err := k.Fetch(ctx, d.GH, []string{id})
	if err != nil {
		return "", err
	}
	if len(items) != 1 || items[0].Error != "" {
		msg := "not found"
		if len(items) == 1 {
			msg = items[0].Error
		}
		return "", fmt.Errorf("%s: %s", id, msg)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	changed, err := watchlist.Add(d.Paths.Watchlist(), id, d.parse)
	if err != nil {
		return "", err
	}
	if !changed {
		return "already watching " + id, nil
	}
	if err := d.syncWatchlistLocked(); err != nil {
		return "", err
	}
	d.applyLocked(items[0])
	d.publishLocked()
	return "watching " + id, nil
}

func (d *Daemon) unwatch(input string) (string, error) {
	id, err := d.parse(input)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	changed, err := watchlist.Remove(d.Paths.Watchlist(), id, d.parse)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", fmt.Errorf("not watching %s", id)
	}
	if err := d.syncWatchlistLocked(); err != nil {
		return "", err
	}
	d.publishLocked()
	return "unwatched " + id, nil
}

// syncWatchlistLocked reloads the file the daemon itself just wrote.
func (d *Daemon) syncWatchlistLocked() error {
	path := d.Paths.Watchlist()
	ids, _, err := watchlist.Load(path, d.parse)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		d.watchStat = fileStat{mod: fi.ModTime(), size: fi.Size(), ok: true}
	}
	d.setIDsLocked(ids)
	return nil
}

func (d *Daemon) retest(ctx context.Context, id string) (string, error) {
	d.mu.Lock()
	it := d.snap.Find(id)
	var item model.Item
	if it != nil {
		item = *it
	}
	d.mu.Unlock()
	if it == nil {
		return "", fmt.Errorf("not watching %s", id)
	}
	r, ok := d.Kinds.Of(id).(kind.Retester)
	if !ok {
		return "", fmt.Errorf("%s items cannot be re-tested", item.Kind)
	}
	info, err := r.Retest(ctx, d.GH, item)
	if info != "" || err == nil {
		d.RequestPoll()
	}
	return info, err
}
