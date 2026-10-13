package model

import (
	"strings"
	"testing"
	"time"
)

func chk(name string, s State) Check { return Check{Name: name, Source: "Actions", State: s} }

func TestAggregate(t *testing.T) {
	req := func(c Check) Check { c.Required = true; return c }
	tests := []struct {
		name   string
		checks []Check
		want   State
	}{
		{"none", nil, Pending},
		{"all passed", []Check{chk("a", Passed), chk("b", Skipped)}, Passed},
		{"one running", []Check{chk("a", Passed), chk("b", Running)}, Running},
		{"pending counts as running", []Check{chk("a", Pending)}, Running},
		{"failed wins", []Check{chk("a", Running), chk("b", Failed)}, Failed},
		{"cancelled is not a failure", []Check{chk("a", Cancelled), chk("b", Passed)}, Passed},
		{"optional failure ignored", []Check{req(chk("a", Passed)), chk("b", Failed)}, Passed},
		{"optional running ignored", []Check{req(chk("a", Passed)), chk("b", Running)}, Passed},
		{"required failure", []Check{req(chk("a", Failed)), chk("b", Passed)}, Failed},
	}
	for _, tt := range tests {
		if got := Aggregate(tt.checks); got != tt.want {
			t.Errorf("%s: Aggregate = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestItemStateFinished(t *testing.T) {
	it := Item{Lifecycle: LifeMerged, Checks: []Check{chk("a", Failed)}}
	if got := ItemState(it); got != Merged {
		t.Errorf("ItemState = %s, want merged", got)
	}
}

func item(sha string, checks ...Check) Item {
	return Item{ID: "pr:o/r#1", HeadSHA: sha, Lifecycle: Open, URL: "https://x/pr", Checks: checks}
}

func types(ts []Transition) []EventType {
	var out []EventType
	for _, t := range ts {
		out = append(out, t.Type)
	}
	return out
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name string
		prev *Item
		cur  Item
		want []EventType
	}{
		{"first sight", nil, item("a", chk("x", Failed)), nil},
		{"no change", ptr(item("a", chk("x", Running))), item("a", chk("x", Running)), nil},
		{"started", ptr(item("a", chk("x", Pending))), item("a", chk("x", Running)), []EventType{EventCheckStarted}},
		{"new check running", ptr(item("a")), item("a", chk("x", Running)), []EventType{EventCheckStarted}},
		{"failed", ptr(item("a", chk("x", Running), chk("y", Running))), item("a", chk("x", Failed), chk("y", Running)), []EventType{EventCheckFailed}},
		{"all passed", ptr(item("a", chk("x", Passed), chk("y", Running))), item("a", chk("x", Passed), chk("y", Passed)), []EventType{EventCheckFinished, EventAllPassed}},
		{"cancelled", ptr(item("a", chk("x", Running), chk("y", Running))), item("a", chk("x", Cancelled), chk("y", Running)), []EventType{EventCheckFinished}},
		{"rerun fails again", ptr(item("a", chk("x", Running))), item("a", chk("x", Failed)), []EventType{EventCheckFailed}},
		{"new push", ptr(item("a", chk("x", Failed))), item("b", chk("x", Pending)), []EventType{EventRestarted}},
	}
	for _, tt := range tests {
		got := types(Diff(tt.prev, tt.cur))
		if len(got) != len(tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		}
	}

	merged := item("a", chk("x", Passed))
	merged.Lifecycle = LifeMerged
	got := Diff(ptr(item("a", chk("x", Passed))), merged)
	if len(got) != 1 || got[0].Type != EventFinished || got[0].To != Merged {
		t.Errorf("merge: got %+v", got)
	}
}

func TestDiffCarriesURL(t *testing.T) {
	c := chk("x", Failed)
	c.URL = "https://prow/job/1"
	got := Diff(ptr(item("a", chk("x", Running))), item("a", c))
	if len(got) != 1 || got[0].URL != c.URL || got[0].Check != "x" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeOrder(t *testing.T) {
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	b := chk("b", Running)
	b.StartedAt = &start
	prev := item("a", b, chk("a", Pending))
	// GitHub reports them in another order, a new check appears first,
	// and the running status lost its start time.
	cur := item("a", chk("new", Pending), chk("a", Running), chk("b", Passed))
	got := MergeOrder(&prev, cur)
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	if names[0] != "b" || names[1] != "a" || names[2] != "new" {
		t.Fatalf("order = %v, want [b a new]", names)
	}
	if got[0].StartedAt == nil || !got[0].StartedAt.Equal(start) {
		t.Errorf("start time not carried over: %v", got[0].StartedAt)
	}

	// A new head starts over in reported order.
	next := item("z", chk("new", Pending), chk("a", Running))
	got = MergeOrder(&prev, next)
	if got[0].Name != "new" || got[0].StartedAt != nil {
		t.Errorf("new head: got %+v", got)
	}
}

func ptr(i Item) *Item { return &i }

func TestItemStateQueued(t *testing.T) {
	it := Item{Lifecycle: Open, MergeQueue: &MergeQueue{State: "queued", Position: 1}, Checks: []Check{chk("a", Passed)}}
	if got := ItemState(it); got != Queued {
		t.Errorf("ItemState = %s, want queued", got)
	}
	it.MergeQueue = nil
	if got := ItemState(it); got != Passed {
		t.Errorf("after leaving the queue: %s, want passed", got)
	}
}

// TestIconsAreWidelyAvailable keeps the safe icons to characters that
// common monospace fonts have (checked against DejaVu Sans Mono, Noto Sans
// Mono, Liberation Mono, Fira Code, JetBrains Mono, Source Code Pro and
// Ubuntu Mono): ASCII, plus a few symbols present in all of them. The
// ASCII set must be pure ASCII.
func TestIconsAreWidelyAvailable(t *testing.T) {
	allowed := "√×–ø"
	for s, icon := range SafeIcons {
		for _, r := range icon {
			if r >= 0x80 && !strings.ContainsRune(allowed, r) {
				t.Errorf("safe %s icon %q uses %U, which many fonts lack", s, icon, r)
			}
		}
	}
	for s, icon := range ASCIIIcons {
		for _, r := range icon {
			if r >= 0x80 {
				t.Errorf("ascii %s icon %q is not ASCII", s, icon)
			}
		}
	}
}

func TestIconSets(t *testing.T) {
	defer UseIcons("fancy")
	all := []State{Pending, Running, Passed, Failed, Skipped, Cancelled, Merged, Closed, Queued}
	for name, set := range IconSets {
		for _, s := range all {
			if set[s] == "" {
				t.Errorf("%s set has no icon for %s", name, s)
			}
		}
	}
	if Failed.Icon() != "✗" || Merged.Icon() != "M" {
		t.Errorf("default set: failed %q, merged %q", Failed.Icon(), Merged.Icon())
	}
	if err := UseIcons("safe"); err != nil || Failed.Icon() != "×" {
		t.Errorf("safe: %v, failed %q", err, Failed.Icon())
	}
	if err := UseIcons("ascii"); err != nil || Passed.Icon() != "v" {
		t.Errorf("ascii: %v, passed %q", err, Passed.Icon())
	}
	if err := UseIcons("emoji"); err == nil {
		t.Error("unknown set accepted")
	}
}

func TestNoteworthy(t *testing.T) {
	merged := item("a", chk("x", Passed))
	merged.Lifecycle = LifeMerged
	tests := []struct {
		name string
		prev *Item
		cur  Item
		want bool
	}{
		{"first sight", nil, item("a", chk("x", Failed)), false},
		{"same state", ptr(item("a", chk("x", Running))), item("a", chk("x", Running), chk("y", Running)), false},
		{"pending to running", ptr(item("a")), item("a", chk("x", Running)), false},
		{"running to failed", ptr(item("a", chk("x", Running))), item("a", chk("x", Failed)), true},
		{"running to passed", ptr(item("a", chk("x", Running))), item("a", chk("x", Passed)), true},
		{"new push", ptr(item("a", chk("x", Passed))), item("b", chk("x", Passed)), true},
		{"merged", ptr(item("a", chk("x", Passed))), merged, true},
	}
	for _, tt := range tests {
		if got := Noteworthy(tt.prev, tt.cur); got != tt.want {
			t.Errorf("%s: Noteworthy = %v, want %v", tt.name, got, tt.want)
		}
	}
}
