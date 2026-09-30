// Package model holds the generic types shared by every part of ghwatch:
// items, checks, check states, transitions and the state snapshot.
//
// The snapshot is a public contract (see docs/snapshot.md). Fields may be
// added in a backwards compatible way; any breaking change bumps
// SchemaVersion.
package model

import (
	"sort"
	"time"
)

// SchemaVersion is the version of the snapshot JSON schema.
const SchemaVersion = 1

// State is the normalized state of a check, or the aggregate state of an item.
type State string

const (
	Pending   State = "pending"
	Running   State = "running"
	Passed    State = "passed"
	Failed    State = "failed"
	Skipped   State = "skipped"
	Cancelled State = "cancelled"

	// Item-only final states, shown instead of the aggregate once an item
	// is finished.
	Merged State = "merged"
	Closed State = "closed"
	// Queued is an open item waiting in a merge queue.
	Queued State = "queued"
)

// Icon returns the single-glyph representation of a state.
func (s State) Icon() string {
	switch s {
	case Pending:
		return "◌"
	case Running:
		return "●"
	case Passed:
		return "✓"
	case Failed:
		return "✗"
	case Skipped:
		return "–"
	case Cancelled:
		return "⊘"
	case Merged:
		return "⮌"
	case Closed:
		return "⊗"
	case Queued:
		return "⧗"
	}
	return "?"
}

// Done reports whether a check in this state has finished.
func (s State) Done() bool {
	switch s {
	case Passed, Failed, Skipped, Cancelled:
		return true
	}
	return false
}

// Lifecycle is where an item is in its life.
type Lifecycle string

const (
	Open       Lifecycle = "open"
	LifeMerged Lifecycle = "merged"
	LifeClosed Lifecycle = "closed"
)

// Finished reports whether the item will not receive new CI.
func (l Lifecycle) Finished() bool { return l == LifeMerged || l == LifeClosed }

// Check is one unit of CI reported on an item's head.
type Check struct {
	Name        string     `json:"name"`
	Source      string     `json:"source"` // "Actions", "Prow", or another status/app name
	State       State      `json:"state"`
	Required    bool       `json:"required"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	URL         string     `json:"url,omitempty"`
}

// Key identifies a check within an item head.
func (c Check) Key() string { return c.Source + "\x00" + c.Name }

// Item is one watched thing (v1: a pull request).
type Item struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	URL       string    `json:"url"`
	Branch    string    `json:"branch,omitempty"`
	HeadSHA   string    `json:"head_sha"`
	PushedAt  time.Time `json:"pushed_at"`
	Lifecycle Lifecycle `json:"lifecycle"`
	State     State     `json:"state"`
	Alerts    bool      `json:"alerts"`
	// WatchedChecks names the checks (jobs) with their own notifications:
	// each of their state changes notifies, even with Alerts off. They are
	// kept across new pushes, since re-run jobs keep their names.
	WatchedChecks []string `json:"watched_checks,omitempty"`
	Checks        []Check  `json:"checks"`
	// MergeQueue is set while the item waits in a merge queue.
	MergeQueue *MergeQueue `json:"merge_queue,omitempty"`
	// Error is set when the last fetch of this item failed; the rest of
	// the item is then the last known state.
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Watching reports whether the check named name has its own notifications.
func (it Item) Watching(name string) bool {
	for _, n := range it.WatchedChecks {
		if n == name {
			return true
		}
	}
	return false
}

// MergeQueue is an item's entry in a merge queue.
type MergeQueue struct {
	// State is GitHub's state of the entry, lower case: queued,
	// awaiting_checks, mergeable, unmergeable or locked.
	State string `json:"state"`
	// Position is the place in line, 1 being next to merge.
	Position   int       `json:"position"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	// ETASeconds is GitHub's estimate of the time left until merge, in
	// seconds; 0 when GitHub gives none.
	ETASeconds int `json:"eta_seconds,omitempty"`
}

// EventType is a kind of notification-worthy event.
type EventType string

const (
	EventCheckFailed  EventType = "check_failed"
	EventAllPassed    EventType = "all_passed"
	EventCheckStarted EventType = "check_started"
	EventRestarted    EventType = "ci_restarted"
	EventFinished     EventType = "item_finished"
	// EventCheckFinished is a check ending other than by failing (passed,
	// skipped, cancelled). It only notifies for watched checks, so it is
	// not in EventTypes.
	EventCheckFinished EventType = "check_finished"
)

// EventTypes lists every event type in display order.
var EventTypes = []EventType{EventCheckFailed, EventAllPassed, EventCheckStarted, EventRestarted, EventFinished}

// Label is the human description of an event type.
func (e EventType) Label() string {
	switch e {
	case EventCheckFailed:
		return "check failed"
	case EventAllPassed:
		return "all checks passed"
	case EventCheckStarted:
		return "check started"
	case EventRestarted:
		return "CI restarted by a new push"
	case EventFinished:
		return "merged or closed"
	}
	return string(e)
}

// Settings are the shared notification settings.
type Settings struct {
	Mute   bool               `json:"mute"`
	Events map[EventType]bool `json:"events"`
}

// DefaultSettings enables every event type: turning alerts on for an item
// notifies about everything until the user narrows it down.
func DefaultSettings() Settings {
	s := Settings{Events: map[EventType]bool{}}
	for _, e := range EventTypes {
		s.Events[e] = true
	}
	return s
}

// Snapshot is the complete daemon state as seen by clients.
type Snapshot struct {
	Schema      int       `json:"schema"`
	GeneratedAt time.Time `json:"generated_at"`
	// PolledAt is when GitHub was last polled successfully; zero before
	// the first successful poll.
	PolledAt time.Time `json:"polled_at,omitzero"`
	Stale    bool      `json:"stale"`
	Error    string    `json:"error,omitempty"`
	Items    []Item    `json:"items"`
	Settings Settings  `json:"settings"`
}

// Find returns the item with the given ID, or nil.
func (s *Snapshot) Find(id string) *Item {
	for i := range s.Items {
		if s.Items[i].ID == id {
			return &s.Items[i]
		}
	}
	return nil
}

// Aggregate computes an item's state from its checks: Failed if any
// counted check failed, else Running if any is pending or running, else
// Passed. When at least one check is marked required, only required
// checks count, so a failing optional job does not turn the item red.
// An item without checks is Pending.
func Aggregate(checks []Check) State {
	if len(checks) == 0 {
		return Pending
	}
	anyRequired := false
	for _, c := range checks {
		if c.Required {
			anyRequired = true
			break
		}
	}
	failed, active := false, false
	for _, c := range checks {
		if anyRequired && !c.Required {
			continue
		}
		switch c.State {
		case Failed:
			failed = true
		case Pending, Running:
			active = true
		}
	}
	switch {
	case failed:
		return Failed
	case active:
		return Running
	}
	return Passed
}

// ItemState returns the state to display for an item: its final lifecycle
// state when finished, Queued while it waits in a merge queue, otherwise
// the aggregate of its checks.
func ItemState(it Item) State {
	switch it.Lifecycle {
	case LifeMerged:
		return Merged
	case LifeClosed:
		return Closed
	}
	if it.MergeQueue != nil {
		return Queued
	}
	return Aggregate(it.Checks)
}

// Progress returns how many checks are done and how many failed.
func Progress(checks []Check) (done, failed int) {
	for _, c := range checks {
		if c.State.Done() {
			done++
		}
		if c.State == Failed {
			failed++
		}
	}
	return
}

// Transition is a change in a check's (or an item's) state between two
// polls. Check is empty for item-level transitions.
type Transition struct {
	Type   EventType `json:"type"`
	ItemID string    `json:"item_id"`
	Check  string    `json:"check,omitempty"`
	From   State     `json:"from,omitempty"`
	To     State     `json:"to,omitempty"`
	URL    string    `json:"url,omitempty"`
}

// Diff compares the previous and current state of one item and returns
// the transitions between them. prev may be nil (first time seen), in
// which case nothing is reported.
func Diff(prev *Item, cur Item) []Transition {
	if prev == nil || prev.HeadSHA == "" || cur.HeadSHA == "" {
		return nil
	}
	var out []Transition
	if !prev.Lifecycle.Finished() && cur.Lifecycle.Finished() {
		return append(out, Transition{Type: EventFinished, ItemID: cur.ID, From: ItemState(*prev), To: ItemState(cur), URL: cur.URL})
	}
	if prev.HeadSHA != cur.HeadSHA {
		return append(out, Transition{Type: EventRestarted, ItemID: cur.ID, URL: cur.URL})
	}
	old := map[string]Check{}
	for _, c := range prev.Checks {
		old[c.Key()] = c
	}
	for _, c := range cur.Checks {
		o, seen := old[c.Key()]
		from := State("")
		if seen {
			from = o.State
		}
		if from == c.State {
			continue
		}
		switch c.State {
		case Failed:
			out = append(out, Transition{Type: EventCheckFailed, ItemID: cur.ID, Check: c.Name, From: from, To: c.State, URL: c.URL})
		case Running:
			out = append(out, Transition{Type: EventCheckStarted, ItemID: cur.ID, Check: c.Name, From: from, To: c.State, URL: c.URL})
		case Passed, Skipped, Cancelled:
			out = append(out, Transition{Type: EventCheckFinished, ItemID: cur.ID, Check: c.Name, From: from, To: c.State, URL: c.URL})
		}
	}
	if pa, ca := Aggregate(prev.Checks), Aggregate(cur.Checks); ca == Passed && pa != Passed && len(cur.Checks) > 0 {
		out = append(out, Transition{Type: EventAllPassed, ItemID: cur.ID, From: pa, To: ca, URL: cur.URL})
	}
	return out
}

// MergeOrder returns cur's checks in a stable order: checks already known
// on the same head keep their position, new checks are appended in the
// order they were reported. Timestamps missing from cur are carried over
// from prev. On a new head the list starts over in reported order.
func MergeOrder(prev *Item, cur Item) []Check {
	checks := append([]Check(nil), cur.Checks...)
	if prev == nil || prev.HeadSHA != cur.HeadSHA {
		return checks
	}
	pos := map[string]int{}
	byKey := map[string]Check{}
	for i, c := range prev.Checks {
		pos[c.Key()] = i
		byKey[c.Key()] = c
	}
	for i := range checks {
		o, ok := byKey[checks[i].Key()]
		if !ok {
			continue
		}
		if checks[i].StartedAt == nil {
			checks[i].StartedAt = o.StartedAt
		}
		if checks[i].CompletedAt == nil && checks[i].State.Done() && o.State.Done() {
			checks[i].CompletedAt = o.CompletedAt
		}
	}
	sort.SliceStable(checks, func(i, j int) bool {
		pi, iok := pos[checks[i].Key()]
		pj, jok := pos[checks[j].Key()]
		switch {
		case iok && jok:
			return pi < pj
		case iok:
			return true
		}
		return false
	})
	return checks
}
