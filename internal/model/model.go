// Package model holds the generic types shared by every part of ghwatch:
// items, checks, check states, transitions and the state snapshot.
//
// The snapshot is a public contract (see docs/snapshot.md). Fields may be
// added in a backwards compatible way; any breaking change bumps
// SchemaVersion.
package model

import (
	"fmt"
	"sort"
	"strings"
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

// Icon sets. PR-level states (merged, closed, queued) are letters in
// every set, so they never depend on the font.
var (
	// FancyIcons are Unicode symbols: the default. Several are missing
	// from common monospace fonts and rely on the terminal borrowing
	// them from another font.
	FancyIcons = map[State]string{
		Pending: "◌", Running: "●", Passed: "✓", Failed: "✗", Skipped: "–", Cancelled: "⊘",
		Merged: "M", Closed: "C", Queued: "Q",
	}
	// SafeIcons are present in (nearly) all common monospace fonts.
	SafeIcons = map[State]string{
		Pending: "o", Running: "*", Passed: "√", Failed: "×", Skipped: "–", Cancelled: "ø",
		Merged: "M", Closed: "C", Queued: "Q",
	}
	// ASCIIIcons are for terminals that cannot show anything else, such
	// as a non-UTF-8 locale or the Linux console.
	ASCIIIcons = map[State]string{
		Pending: "o", Running: "*", Passed: "v", Failed: "x", Skipped: "-", Cancelled: "/",
		Merged: "M", Closed: "C", Queued: "Q",
	}
)

// IconSets maps the names accepted by UseIcons to their sets.
var IconSets = map[string]map[State]string{"fancy": FancyIcons, "safe": SafeIcons, "ascii": ASCIIIcons}

// icons is the set in use, and iconSet its name. They are chosen once,
// at startup.
var (
	icons   = FancyIcons
	iconSet = "fancy"
)

// IconSet returns the name of the icon set in use.
func IconSet() string { return iconSet }

// UseIcons selects the icon set by name: fancy, safe or ascii.
func UseIcons(name string) error {
	set, ok := IconSets[name]
	if !ok {
		return fmt.Errorf("unknown icon set %q (want fancy or safe)", name)
	}
	icons, iconSet = set, name
	return nil
}

// Icon returns the single-glyph representation of a state in the icon set
// in use.
func (s State) Icon() string {
	if i, ok := icons[s]; ok {
		return i
	}
	return "?"
}

// CommentIcon marks new comments: the Nerd Fonts codicon comment
// (nf-cod-comment) with the fancy icons, a letter with the others.
func CommentIcon() string {
	if iconSet == "fancy" {
		return "\uea6b"
	}
	return "c"
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
	// DetailsURL is the reporting app's own page for the check (for
	// example a Konflux pipeline run), when it differs from URL. For check
	// runs, URL is GitHub's check page, as on the pull request page.
	DetailsURL string `json:"details_url,omitempty"`
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
	// WatchedThreads names the comment threads (by Thread.ID) with their
	// own notifications: each new comment in them notifies.
	WatchedThreads []string `json:"watched_threads,omitempty"`
	// Muted silences all of the item's notifications, its Alerts and its
	// watched checks and threads, while keeping them to restore on unmute.
	Muted bool `json:"muted,omitempty"`
	// Review is where the user stands as a reviewer of the item.
	Review MyReview `json:"review,omitzero"`
	// Added says why ghwatch added the item on its own (AddedReview,
	// AddedAssigned); empty for items the user added.
	Added []string `json:"added,omitempty"`
	// Threads are the item's comment threads: its conversation, then its
	// review threads on code.
	Threads []Thread `json:"threads,omitempty"`
	// CommentsReadAt is when a user last read the item's comments: newer
	// comments are new. Zero until the comments are first fetched.
	CommentsReadAt time.Time `json:"comments_read_at,omitzero"`
	// Unseen is set when the item changed in a way worth a look (its
	// state, a new push, a merge) and cleared once a user looked at it.
	Unseen bool    `json:"unseen,omitempty"`
	Checks []Check `json:"checks"`
	// Labels are the item's labels, in GitHub's order.
	Labels []Label `json:"labels,omitempty"`
	// MergeQueue is set while the item waits in a merge queue.
	MergeQueue *MergeQueue `json:"merge_queue,omitempty"`
	// Error is set when the last fetch of this item failed; the rest of
	// the item is then the last known state.
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Noteworthy reports whether the change from prev to cur is worth a look:
// a new push, or a different displayed state. Pending and running count
// as the same, so a PR starting its checks is not one.
func Noteworthy(prev *Item, cur Item) bool {
	if prev == nil || prev.HeadSHA == "" {
		return false
	}
	busy := func(s State) State {
		if s == Pending {
			return Running
		}
		return s
	}
	return prev.HeadSHA != cur.HeadSHA || busy(ItemState(*prev)) != busy(ItemState(cur))
}

// HasAlerts reports whether any notification is set on the item, muted
// or not: its own alerts, a job's or a comment thread's.
func (it Item) HasAlerts() bool {
	return it.Alerts || len(it.WatchedChecks) > 0 || len(it.WatchedThreads) > 0
}

// WatchingThread reports whether the thread with the given ID has its
// own notifications.
func (it Item) WatchingThread(id string) bool {
	for _, n := range it.WatchedThreads {
		if n == id {
			return true
		}
	}
	return false
}

// NewComments counts the comments newer than CommentsReadAt.
func (it Item) NewComments() int {
	n := 0
	for _, t := range it.Threads {
		n += t.NewSince(it.CommentsReadAt)
	}
	return n
}

// LatestComment returns when the newest counted comment was written.
func (it Item) LatestComment() time.Time {
	var last time.Time
	for _, t := range it.Threads {
		for _, c := range t.Comments {
			if c.Counts() && c.CreatedAt.After(last) {
				last = c.CreatedAt
			}
		}
	}
	return last
}

// MyReview is the user's part in an item's review.
type MyReview struct {
	// Requested is set while the user's review is requested; Team names
	// the team it was requested from, when not from the user directly.
	Requested bool   `json:"requested,omitempty"`
	Team      string `json:"team,omitempty"`
	// State is the user's latest review, lower case: approved,
	// changes_requested, commented, dismissed, or pending (a draft not
	// submitted yet). Empty when the user has not reviewed the item.
	State string    `json:"state,omitempty"`
	At    time.Time `json:"at,omitzero"`
}

// Reviewed reports whether the user submitted a review.
func (r MyReview) Reviewed() bool { return r.State != "" && r.State != "pending" }

// Reasons for adding an item automatically.
const (
	AddedReview   = "review requested"
	AddedAssigned = "assigned"
)

// ConversationID is the Thread.ID of an item's main conversation.
const ConversationID = "conversation"

// Thread is a discussion on an item: its main conversation (comments and
// review summaries), or a review thread on a line of code.
type Thread struct {
	// ID is ConversationID or GitHub's ID of the review thread.
	ID       string `json:"id"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
	Outdated bool   `json:"outdated,omitempty"`
	// Total is the number of comments in the thread; Comments holds only
	// the latest ones, oldest first.
	Total    int       `json:"total"`
	Comments []Comment `json:"comments"`
	// URL is the thread's page; a comment's own URL is preferred.
	URL string `json:"url,omitempty"`
}

// Name is how the thread is shown: "conversation", or the file and line
// of a review thread.
func (t Thread) Name() string {
	switch {
	case t.ID == ConversationID:
		return "conversation"
	case t.Line > 0:
		return fmt.Sprintf("%s:%d", t.Path, t.Line)
	case t.Path != "":
		return t.Path
	}
	return "review thread"
}

// Last returns the thread's latest comment, or nil.
func (t Thread) Last() *Comment {
	if len(t.Comments) == 0 {
		return nil
	}
	return &t.Comments[len(t.Comments)-1]
}

// Link is where to read the thread: its latest comment.
func (t Thread) Link() string {
	if c := t.Last(); c != nil && c.URL != "" {
		return c.URL
	}
	return t.URL
}

// NewSince counts the thread's counted comments written after since.
func (t Thread) NewSince(since time.Time) int {
	n := 0
	for _, c := range t.Comments {
		if c.Counts() && c.CreatedAt.After(since) {
			n++
		}
	}
	return n
}

// Comment is one comment, or a review summary, in a thread.
type Comment struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	// Body is the comment's text (as GitHub renders it to plain text), cut
	// at BodyLimit characters.
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url,omitempty"`
	// Review is the state of a review summary, lower case: approved,
	// changes_requested, commented or dismissed. Empty for comments.
	Review string `json:"review,omitempty"`
	// Mine is set on comments by the user ghwatch runs as.
	Mine bool `json:"mine,omitempty"`
	// Ignored is set on comments by authors in the ignore_authors list.
	Ignored bool `json:"ignored,omitempty"`
}

// Counts reports whether the comment can be new and notify: it is
// neither the user's own nor by an ignored author.
func (c Comment) Counts() bool { return !c.Mine && !c.Ignored }

// BodyLimit caps a comment's stored text, so a huge comment (a CI bot's
// report, say) does not bloat the snapshot.
const BodyLimit = 4000

// Summary is the comment's first non-empty line.
func (c Comment) Summary() string {
	for _, l := range strings.Split(c.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
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

// Label is a label on an item.
type Label struct {
	Name string `json:"name"`
	// Color is GitHub's color for the label, as six hex digits ("d73a4a").
	Color string `json:"color,omitempty"`
}

// MergeQueue is an item's entry in a merge queue.
type MergeQueue struct {
	// State is GitHub's state of the entry, lower case: queued,
	// awaiting_checks, mergeable, unmergeable or locked.
	State string `json:"state"`
	// Position is the place in line, 1 being next to merge.
	Position   int       `json:"position"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	// URL is the merge queue's page.
	URL string `json:"url,omitempty"`
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
	// EventComment is a new comment, in the conversation or on code.
	EventComment EventType = "comment"
)

// EventTypes lists every event type in display order.
var EventTypes = []EventType{EventCheckFailed, EventAllPassed, EventCheckStarted, EventRestarted, EventFinished, EventComment}

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
	case EventComment:
		return "new comment"
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
	// Notice is a message for the user about something the daemon did on
	// its own (auto-add adding or removing PRs), from NoticeAt.
	Notice   string    `json:"notice,omitempty"`
	NoticeAt time.Time `json:"notice_at,omitzero"`
	// Dismissed are the IDs of auto-added items the user removed: they
	// are not added again.
	Dismissed []string `json:"dismissed,omitempty"`
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
	// Thread, Where and Comment describe a new comment: its thread's ID
	// and name, and the comment itself.
	Thread  string   `json:"thread,omitempty"`
	Where   string   `json:"where,omitempty"`
	Comment *Comment `json:"comment,omitempty"`
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

// CommentDiff returns a transition for every counted comment of cur that
// prev did not have. Nothing is reported until prev had its comments
// fetched (CommentsReadAt is set then), so existing comments of a newly
// watched item, or of one saved by an older ghwatch, are not new.
func CommentDiff(prev *Item, cur Item) []Transition {
	if prev == nil || prev.CommentsReadAt.IsZero() {
		return nil
	}
	known := map[string]bool{}
	oldest := map[string]time.Time{} // per thread, its oldest fetched comment
	for _, t := range prev.Threads {
		for i, c := range t.Comments {
			known[c.ID] = true
			if i == 0 {
				oldest[t.ID] = c.CreatedAt
			}
		}
	}
	var out []Transition
	for _, t := range cur.Threads {
		for _, c := range t.Comments {
			if known[c.ID] || !c.Counts() {
				continue
			}
			// Only the latest comments of a thread are fetched: an older
			// one shows up when a newer one is deleted, and is not new.
			if o, ok := oldest[t.ID]; ok && c.CreatedAt.Before(o) {
				continue
			}
			c := c
			out = append(out, Transition{Type: EventComment, ItemID: cur.ID, URL: c.URL, Thread: t.ID, Where: t.Name(), Comment: &c})
		}
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
