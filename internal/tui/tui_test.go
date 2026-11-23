package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/model"
)

type fakeBackend struct {
	sent   []ipc.Command
	opened []string
	copied []string
	// edits are the texts the fake editor returns, in turn; edited
	// records the texts it was given.
	edits  []string
	edited []string
}

func (f *fakeBackend) Edit(text string) (string, error) {
	f.edited = append(f.edited, text)
	if len(f.edits) == 0 {
		return text, errors.New("no editor")
	}
	out := f.edits[0]
	f.edits = f.edits[1:]
	return out, nil
}

func (f *fakeBackend) Send(c ipc.Command)  { f.sent = append(f.sent, c) }
func (f *fakeBackend) Open(u string) error { f.opened = append(f.opened, u); return nil }
func (f *fakeBackend) Copy(s string) error { f.copied = append(f.copied, s); return nil }

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { t := now.Add(-d); return &t }

func snapshot() *model.Snapshot {
	items := []model.Item{
		{ID: "pr:org/repo#123", Repo: "org/repo", Number: 123, Title: "Fix lease renewal race", Author: "clobrano",
			Branch: "fix-lease-race", HeadSHA: "a1b2c3d4e5", PushedAt: now.Add(-52 * time.Minute), Lifecycle: model.Open,
			URL: "https://github.com/org/repo/pull/123", Alerts: true,
			Checks: []model.Check{
				{Name: "lint", Source: "Actions", State: model.Passed, StartedAt: at(10 * time.Minute), CompletedAt: at(8 * time.Minute), URL: "u/lint", Required: true},
				{Name: "e2e-aws-ovn", Source: "Prow", State: model.Failed, URL: "u/e2e", Required: true},
				{Name: "e2e-metal-ipi", Source: "Prow", State: model.Running, StartedAt: at(23 * time.Minute), URL: "u/metal"},
				{Name: "tide", Source: "Prow", State: model.Pending},
			}},
		{ID: "pr:org/repo#131", Repo: "org/repo", Number: 131, Title: "SBD timeout", Branch: "sbd-timeout", HeadSHA: "b", Lifecycle: model.Open,
			Checks: []model.Check{{Name: "unit", Source: "Actions", State: model.Running}}},
		{ID: "pr:osac/osac#58", Repo: "osac/osac", Number: 58, Title: "Docs", HeadSHA: "c", Lifecycle: model.LifeMerged},
	}
	for i := range items {
		items[i].State = model.ItemState(items[i])
	}
	return &model.Snapshot{Schema: 1, Items: items, Settings: model.DefaultSettings()}
}

func newModel() (*Model, *fakeBackend) {
	be := &fakeBackend{}
	m := NewModel(be)
	m.view = viewChecks // most tests are about the checks list
	m.now = func() time.Time { return now }
	m.Color = false
	m.SetConnected(true)
	m.SetSnapshot(snapshot())
	return m, be
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		m.Key(k)
	}
}

func TestView(t *testing.T) {
	m, _ := newModel()
	screen := strings.Join(m.View(90, 16), "\n")
	for _, want := range []string{
		"× #123 lease-race " + bellIcon,
		"* #131 sbd-timeout",
		"M osac#58 docs",
		"[OPEN] Fix lease renewal race  org/repo#123",
		"@clobrano",
		" CI:       2/4 done · 1 failing, all required · 2 running · on a1b2c3d, pushed 52m ago",
		" alerts:   on for the PR",
		"running 23m",
		"pending",
		" opt",
		"connected",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	rows := m.View(90, 18)
	if len(rows) != 18 {
		t.Errorf("View returned %d rows", len(rows))
	}
	// Failed first, then running (and pending), then passed, each under a
	// header; the first failure is selected.
	want := []string{" Failed · 1", "›× e2e-aws-ovn", " Running · 2", " * e2e-metal-ipi", " o tide", " Passed · 1", " √ lint"}
	body := listRows(rows)
	for i, prefix := range want {
		if i >= len(body) || !strings.HasPrefix(body[i], prefix) {
			t.Errorf("list row %d: want prefix %q in\n%s", i, prefix, strings.Join(body, "\n"))
		}
	}
	top := boxTop(rows)
	if top < 0 || !strings.Contains(rows[top+1], "Check") || !strings.Contains(rows[top+1], "Time") || !strings.Contains(rows[top+1], "Source") {
		t.Errorf("box top at %d:\n%s", top, strings.Join(rows, "\n"))
	}
	// The box ends after its last row; the footer stays at the bottom.
	if !strings.HasPrefix(rows[top+2+len(want)], "└") || !strings.Contains(rows[17], "connected") {
		t.Errorf("box or footer misplaced:\n%s", strings.Join(rows, "\n"))
	}
	if len(body) > 6 && !strings.Contains(body[6], "2m") {
		t.Errorf("lint row lacks its duration: %q", body[6])
	}
}

func TestTabOverflowKeepsActiveVisible(t *testing.T) {
	m, _ := newModel()
	keys(m, "3")
	bar := m.View(30, 10)[1]
	if !strings.Contains(bar, "osac#58") || !strings.HasPrefix(bar, "‹") {
		t.Errorf("tab bar = %q", bar)
	}
	keys(m, "1")
	bar = m.View(30, 10)[1]
	if !strings.Contains(bar, "#123") || !strings.HasSuffix(strings.TrimRight(bar, " "), "›") {
		t.Errorf("tab bar = %q", bar)
	}
}

func TestNavigationAndActions(t *testing.T) {
	m, be := newModel()
	keys(m, "j", kEnter, "o", "Y", "y")
	if !reflect.DeepEqual(be.opened, []string{"u/metal", "https://github.com/org/repo/pull/123"}) {
		t.Errorf("opened = %v", be.opened)
	}
	if !reflect.DeepEqual(be.copied, []string{"u/metal", "https://github.com/org/repo/pull/123"}) {
		t.Errorf("copied = %v", be.copied)
	}
	keys(m, "G")
	if _, c := m.selected(); c.Name != "lint" {
		t.Errorf("G selected %s", c.Name)
	}
	keys(m, "g", "g")
	if _, c := m.selected(); c.Name != "e2e-aws-ovn" {
		t.Errorf("gg selected %s", c.Name)
	}
	keys(m, "l")
	if m.active != "pr:org/repo#131" {
		t.Errorf("l: active = %s", m.active)
	}
	keys(m, "g", "t", "g", "t")
	if m.active != "pr:org/repo#123" {
		t.Errorf("gt wraps: active = %s", m.active)
	}
	keys(m, "g", "T")
	if m.active != "pr:osac/osac#58" {
		t.Errorf("gT wraps: active = %s", m.active)
	}

	// Selection is remembered per tab.
	keys(m, "1", "j", "2", "1")
	if _, c := m.selected(); c.Name != "e2e-metal-ipi" {
		t.Errorf("selection not kept per tab: %s", c.Name)
	}

	keys(m, "n")
	if len(be.sent) != 1 || be.sent[0].Op != ipc.OpAlerts || *be.sent[0].On != false {
		t.Errorf("n sent %+v", be.sent)
	}
	keys(m, "p")
	if be.sent[1].Op != ipc.OpPoll {
		t.Errorf("p sent %+v", be.sent[1])
	}
	keys(m, "d", "n")
	if len(be.sent) != 2 {
		t.Errorf("declined unwatch was sent")
	}
	keys(m, "d", "y")
	if be.sent[2].Op != ipc.OpUnwatch || be.sent[2].ID != "pr:org/repo#123" {
		t.Errorf("d y sent %+v", be.sent[2])
	}
	keys(m, "R", "y")
	if be.sent[3].Op != ipc.OpRetest {
		t.Errorf("R y sent %+v", be.sent[3])
	}
	keys(m, "a", "o", "/", "r", "#", "9", kBackspace, "7", kEnter)
	if be.sent[4].Op != ipc.OpAdd || be.sent[4].ID != "o/r#7" {
		t.Errorf("add sent %+v", be.sent[4])
	}
	keys(m, "q")
	if !m.Quit() {
		t.Error("q did not quit")
	}
}

func TestUnwatchedTabKeepsPosition(t *testing.T) {
	m, _ := newModel()
	keys(m, "2")
	s := snapshot()
	s.Items = append(s.Items[:1], s.Items[2:]...)
	m.SetSnapshot(s)
	if m.active != "pr:osac/osac#58" {
		t.Errorf("active after removal = %s", m.active)
	}
	m.SetSnapshot(&model.Snapshot{})
	if strings.Contains(strings.Join(m.View(80, 10), "\n"), "panic") || m.current() != nil {
		t.Error("empty snapshot not handled")
	}
}

func TestFind(t *testing.T) {
	m, be := newModel()
	// Searches the jobs of the current tab, ignoring case; tabs stay put.
	keys(m, "/", "M", "E", "T", "A", "L")
	rows := m.View(90, 16)
	if body := listRows(rows); !strings.Contains(body[0], "1 of 4 jobs match") || !strings.HasPrefix(body[1], "›* e2e-metal-ipi") {
		t.Errorf("find list =\n%s", strings.Join(body, "\n"))
	}
	if !strings.Contains(rows[15], "/METAL") {
		t.Errorf("footer = %q", rows[15])
	}
	keys(m, kEnter)
	if m.mode != modeNormal || m.active != "pr:org/repo#123" {
		t.Fatalf("mode %v, active %s", m.mode, m.active)
	}
	if _, c := m.selected(); c.Name != "e2e-metal-ipi" {
		t.Errorf("selected %s", c.Name)
	}
	keys(m, kEnter) // enter again opens the job
	if len(be.opened) != 1 || be.opened[0] != "u/metal" {
		t.Errorf("opened %v", be.opened)
	}

	// Several matches: substring matches first, arrows choose.
	keys(m, "/", "e", "2", "e")
	if _, matches := m.findMatches(); len(matches) != 2 {
		t.Fatalf("e2e matches %d jobs", len(matches))
	}
	keys(m, kDown, kEnter)
	if _, c := m.selected(); c.Name != "e2e-metal-ipi" {
		t.Errorf("second e2e match: selected %s", c.Name)
	}

	// No match, or esc: the selection does not move.
	keys(m, "/", "z", "z", "z")
	if !strings.Contains(strings.Join(m.View(90, 16), "\n"), "no matching job") {
		t.Error("no-match message missing")
	}
	keys(m, kEnter, "/", "l", "i", "n", "t", kEsc)
	if _, c := m.selected(); c.Name != "e2e-metal-ipi" {
		t.Errorf("selection moved to %s", c.Name)
	}

	// Jobs of another tab are not searched.
	keys(m, "/", "u", "n", "i", "t", kEnter)
	if m.active != "pr:org/repo#123" {
		t.Errorf("find switched tab to %s", m.active)
	}
}

func TestEventsPanel(t *testing.T) {
	m, be := newModel()
	keys(m, "N")
	screen := strings.Join(m.View(80, 20), "\n")
	if !strings.Contains(screen, "› [x] check failed") || !strings.Contains(screen, "[ ] global mute") {
		t.Errorf("events panel:\n%s", screen)
	}
	keys(m, " ", "k", kEnter, kEsc)
	if len(be.sent) != 2 || be.sent[0].Events[model.EventCheckFailed] != false || be.sent[1].Op != ipc.OpMute || !*be.sent[1].On {
		t.Errorf("sent %+v", be.sent)
	}
	if m.mode != modeNormal {
		t.Error("esc did not close the panel")
	}
}

func TestDisconnected(t *testing.T) {
	m, be := newModel()
	m.SetConnected(false)
	keys(m, "p")
	if len(be.sent) != 0 {
		t.Error("command sent while disconnected")
	}
	footer := m.View(100, 10)[9]
	if !strings.Contains(footer, "disconnected · stale") {
		t.Errorf("footer = %q", footer)
	}
}

func TestParseKeys(t *testing.T) {
	got := parseKeys([]byte("jk\x1b[A\x1b[B\r\x7f\x1b\x03é\x1b[Z\x1b[5~"))
	want := []string{"j", "k", kUp, kDown, kEnter, kBackspace, kEsc, kCtrlC, "é", kShiftTab, kPgUp}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseKeys = %q, want %q", got, want)
	}
	if got := parseKeys([]byte("\x1b")); !reflect.DeepEqual(got, []string{kEsc}) {
		t.Errorf("lone esc = %q", got)
	}
}

func TestTextHelpers(t *testing.T) {
	if got := truncate("e2e-metal-ipi", 6); got != "e2e-m…" {
		t.Errorf("truncate = %q", got)
	}
	if got := padLeft("2m", 5); got != "   2m" {
		t.Errorf("padLeft = %q", got)
	}
	for d, want := range map[time.Duration]string{41 * time.Second: "41s", 23 * time.Minute: "23m", 65 * time.Minute: "1h05m", 50 * time.Hour: "2d2h"} {
		if got := human(d); got != want {
			t.Errorf("human(%s) = %s, want %s", d, got, want)
		}
	}
	if got := slug(model.Item{Branch: "clobrano/1234-fix-lease-renewal-race"}); got != "lease-renewal" {
		t.Errorf("slug = %q", got)
	}
}

func TestHelpFitsInColumns(t *testing.T) {
	// Tall enough: one column, every key and description.
	m, _ := newModel()
	keys(m, "?")
	screen := strings.Join(m.View(120, 40), "\n")
	if !strings.Contains(screen, "[Keybindings]") {
		t.Errorf("no title chip:\n%s", screen)
	}
	for _, sec := range helpSections {
		if !strings.Contains(screen, sec.title) {
			t.Errorf("help lacks section %q", sec.title)
		}
		for _, r := range sec.keys {
			if !strings.Contains(screen, r.desc) {
				t.Errorf("help lacks %q:\n%s", r.desc, screen)
			}
		}
	}
	// Short screen: the sections flow into columns and still all show.
	screen = strings.Join(m.View(200, 20), "\n")
	for _, sec := range helpSections {
		if !strings.Contains(screen, sec.title) {
			t.Errorf("short screen lacks section %q:\n%s", sec.title, screen)
		}
	}
	keys(m, "x")
	if m.mode != modeNormal {
		t.Error("any key should close help")
	}
}

func TestSelectionFollowsCheckAcrossGroups(t *testing.T) {
	m, _ := newModel()
	keys(m, "j") // e2e-metal-ipi, running
	s := snapshot()
	s.Items[0].Checks[2].State = model.Failed
	m.SetSnapshot(s)
	if _, c := m.selected(); c.Name != "e2e-metal-ipi" {
		t.Fatalf("selection moved to %s", c.Name)
	}
	rows := m.View(90, 16)
	if body := listRows(rows); !strings.HasPrefix(body[0], " Failed · 2") || !strings.HasPrefix(body[2], "›× e2e-metal-ipi") {
		t.Errorf("rows =\n%s", strings.Join(body, "\n"))
	}

	// On a new head the check is gone: the selection keeps its position.
	s = snapshot()
	s.Items[0].HeadSHA = "new"
	s.Items[0].Checks = []model.Check{{Name: "unit", Source: "Actions", State: model.Pending}, {Name: "lint", Source: "Actions", State: model.Pending}}
	m.SetSnapshot(s)
	if _, c := m.selected(); c.Name != "lint" {
		t.Errorf("after new head selected %s, want the check at the same position", c.Name)
	}
}

func TestGroupScrollShowsHeader(t *testing.T) {
	m, _ := newModel()
	// 5 list rows for 7 entries: the passed group starts below the fold.
	rows := m.View(90, 16)
	if body := listRows(rows); len(body) != 5 || strings.Contains(strings.Join(body, "\n"), "lint") {
		t.Fatalf("want 5 rows without lint:\n%s", strings.Join(body, "\n"))
	}
	keys(m, "G")
	body := strings.Join(listRows(m.View(90, 16)), "\n")
	if !strings.Contains(body, "Passed · 1") || !strings.Contains(body, "›√ lint") {
		t.Errorf("body =\n%s", body)
	}
	keys(m, "g", "g")
	if first := listRows(m.View(90, 16))[0]; !strings.HasPrefix(first, " Failed · 1") {
		t.Errorf("scrolled back, first body row = %q", first)
	}
}

func TestCancelledGroupAtBottom(t *testing.T) {
	m, _ := newModel()
	s := snapshot()
	s.Items[0].Checks = append([]model.Check{{Name: "stale-job", Source: "Prow", State: model.Cancelled}}, s.Items[0].Checks...)
	m.SetSnapshot(s)
	rows := m.View(90, 20)
	want := []string{" Failed · 1", "›× e2e-aws-ovn", " Running · 2", " * e2e-metal-ipi", " o tide", " Passed · 1", " √ lint", " Cancelled · 1", " ø stale-job"}
	body := listRows(rows)
	for i, prefix := range want {
		if i >= len(body) || !strings.HasPrefix(body[i], prefix) {
			t.Errorf("list row %d: want prefix %q in\n%s", i, prefix, strings.Join(body, "\n"))
		}
	}
}

func TestBelledJobsFirstInGroup(t *testing.T) {
	m, _ := newModel()
	keys(m, "j", "j") // tide, the second running job
	order := func() []string {
		var names []string
		for _, r := range listRows(m.View(90, 20)) {
			if f := strings.Fields(r); len(f) > 1 && !strings.Contains(r, "·") {
				names = append(names, f[1])
			}
		}
		return names
	}

	// Ringing a bell does not move the job under the cursor...
	s := snapshot()
	s.Items[0].WatchedChecks = []string{"tide"}
	m.SetSnapshot(s)
	if got := strings.Join(order(), " "); got != "e2e-aws-ovn e2e-metal-ipi tide lint" {
		t.Errorf("after the bell, order = %s", got)
	}
	// ...the order catches up when the tab is opened again.
	keys(m, "l", "h")
	if got := strings.Join(order(), " "); got != "e2e-aws-ovn tide e2e-metal-ipi lint" {
		t.Errorf("back on the tab, order = %s", got)
	}
	if _, c := m.selected(); c.Name != "tide" {
		t.Errorf("selection moved to %s", c.Name)
	}

	// Silencing it does not move it either.
	m.SetSnapshot(snapshot())
	if got := strings.Join(order(), " "); got != "e2e-aws-ovn tide e2e-metal-ipi lint" {
		t.Errorf("after silencing, order = %s", got)
	}
	keys(m, "l", "h")
	if got := strings.Join(order(), " "); got != "e2e-aws-ovn e2e-metal-ipi tide lint" {
		t.Errorf("silenced, back on the tab, order = %s", got)
	}
}

func TestJobBell(t *testing.T) {
	m, be := newModel()
	keys(m, "j", "b") // e2e-metal-ipi
	if len(be.sent) != 1 || be.sent[0].Op != ipc.OpCheckAlerts || be.sent[0].Check != "e2e-metal-ipi" || !*be.sent[0].On {
		t.Fatalf("b sent %+v", be.sent)
	}

	// Once the daemon confirms, the job shows a bell, as does its tab even
	// with PR alerts off.
	s := snapshot()
	s.Items[0].Alerts = false
	s.Items[0].WatchedChecks = []string{"e2e-metal-ipi"}
	m.SetSnapshot(s)
	rows := m.View(90, 16)
	body := listRows(rows)
	if !strings.HasPrefix(body[3], "›* e2e-metal-ipi "+bellIcon+" ") || !strings.Contains(body[3], "running 23m") {
		t.Errorf("watched job row = %q", body[3])
	}
	if strings.Contains(body[1], bellIcon) {
		t.Errorf("unwatched job has a bell: %q", body[1])
	}
	if a := headerRow(rows, "alerts"); !strings.Contains(rows[1], "#123 lease-race "+bellIcon) || a != " alerts:   "+bellIcon+" 1 job" {
		t.Errorf("tab = %q, alerts row = %q", rows[1], a)
	}
	keys(m, "b")
	if !*be.sent[0].On || *be.sent[1].On {
		t.Errorf("second b should turn the bell off: %+v", be.sent[1])
	}
}

func TestMutePR(t *testing.T) {
	m, be := newModel()
	s := snapshot()
	s.Items[0].Alerts = false
	m.SetSnapshot(s)
	keys(m, "m")
	if len(be.sent) != 0 || !strings.Contains(m.View(90, 16)[15], "no alerts to mute") {
		t.Fatalf("m without alerts sent %+v, footer %q", be.sent, m.View(90, 16)[15])
	}

	s = snapshot()
	s.Items[0].Alerts = true
	s.Items[0].WatchedChecks = []string{"e2e-metal-ipi"}
	m.SetSnapshot(s)
	keys(m, "m")
	if len(be.sent) != 1 || be.sent[0].Op != ipc.OpMuteItem || be.sent[0].ID != s.Items[0].ID || !*be.sent[0].On {
		t.Fatalf("m sent %+v", be.sent)
	}

	// Muted, the PR keeps its alerts and bells, and says so.
	s.Items[0].Muted = true
	m.SetSnapshot(s)
	rows := m.View(90, 16)
	if a := headerRow(rows, "alerts"); a != " alerts:   "+bellIcon+" muted (m restores)" {
		t.Errorf("alerts row = %q", a)
	}
	if !strings.Contains(rows[1], "#123 lease-race "+bellIcon) {
		t.Errorf("tab = %q", rows[1])
	}
	if strings.Contains(rows[0], bellIcon) {
		t.Errorf("title bar counts a muted PR: %q", rows[0])
	}
	keys(m, "m")
	if *be.sent[1].On {
		t.Errorf("second m should restore: %+v", be.sent[1])
	}
}

func TestClearAlerts(t *testing.T) {
	m, be := newModel()
	s := snapshot()
	s.Items[0].Alerts = false
	m.SetSnapshot(s)
	keys(m, "c")
	if m.mode != modeNormal || !strings.Contains(m.View(90, 16)[15], "no alerts set") {
		t.Fatalf("c without alerts: mode %v, footer %q", m.mode, m.View(90, 16)[15])
	}

	s.Items[0].WatchedChecks = []string{"e2e-metal-ipi"}
	m.SetSnapshot(s)
	keys(m, "c", "n")
	if len(be.sent) != 0 {
		t.Errorf("declined clear was sent: %+v", be.sent)
	}
	keys(m, "c", "y")
	if len(be.sent) != 1 || be.sent[0].Op != ipc.OpClearAlerts || be.sent[0].ID != s.Items[0].ID {
		t.Errorf("c y sent %+v", be.sent)
	}
}

func TestOpenShowsFeedback(t *testing.T) {
	m, be := newModel()
	keys(m, kEnter)
	if len(be.opened) != 1 || be.opened[0] != "u/e2e" {
		t.Fatalf("opened %v", be.opened)
	}
	if !strings.Contains(m.View(90, 16)[15], "opening u/e2e") {
		t.Errorf("footer = %q", m.View(90, 16)[15])
	}
	m.Result("", errors.New("xdg-open: exit status 4"))
	if !strings.Contains(m.View(90, 16)[15], "xdg-open: exit status 4") {
		t.Errorf("browser failure not shown: %q", m.View(90, 16)[15])
	}
}

func TestTitleBar(t *testing.T) {
	m := NewModel(&fakeBackend{})
	m.now = func() time.Time { return now }
	m.Color = false
	if got := m.View(80, 10)[0]; !strings.HasPrefix(got, " GHWATCH  GitHub PR watcher") || !strings.Contains(got, "waiting for the daemon") {
		t.Errorf("before any state: %q", got)
	}

	m, _ = newModel()
	s := snapshot()
	s.Items[1].WatchedChecks = []string{"unit"}
	s.PolledAt = now.Add(-42 * time.Second)
	m.SetSnapshot(s)
	got := m.View(100, 16)[0]
	for _, want := range []string{" GHWATCH  GitHub PR watcher · 3 PRs: 1 failing · 1 running · 1 merged", "polled 42s ago "} {
		if !strings.Contains(got, want) {
			t.Errorf("title = %q, lacks %q", got, want)
		}
	}
	if strings.Contains(got, "muted") {
		t.Errorf("title shows muted: %q", got)
	}

	s.Settings.Mute = true
	s.Stale = true
	s.Items = s.Items[:1]
	m.SetSnapshot(s)
	got = m.View(100, 16)[0]
	for _, want := range []string{"watcher · 1 PR: 1 failing · muted", "last poll 42s ago "} {
		if !strings.Contains(got, want) {
			t.Errorf("title = %q, lacks %q", got, want)
		}
	}

	s.PolledAt = time.Time{}
	m.SetSnapshot(s)
	if got := m.View(100, 16)[0]; !strings.Contains(got, "not polled yet") {
		t.Errorf("title = %q", got)
	}
}

func TestMergeQueue(t *testing.T) {
	m, _ := newModel()
	s := snapshot()
	s.Items[1].MergeQueue = &model.MergeQueue{State: "awaiting_checks", Position: 2, EnqueuedAt: now.Add(-12 * time.Minute), ETASeconds: 480}
	s.Items[1].Checks[0].State = model.Passed
	m.SetSnapshot(s)
	keys(m, "2")
	rows := m.View(130, 16)
	if !strings.Contains(rows[0], "3 PRs: 1 failing · 1 queued · 1 merged") {
		t.Errorf("title = %q", rows[0])
	}
	if !strings.Contains(rows[1], "Q #131 sbd-timeout") {
		t.Errorf("tabs = %q", rows[1])
	}
	if !strings.HasPrefix(headerRow(rows, "CI"), " CI:       in merge queue, checks running, 2nd in line, 12m ago, ~8m left · 1/1 done") {
		t.Errorf("CI row = %q", headerRow(rows, "CI"))
	}

	s.Items[1].MergeQueue = &model.MergeQueue{State: "queued", Position: 1}
	m.SetSnapshot(s)
	if got := headerRow(m.View(130, 16), "CI"); !strings.Contains(got, "queued for merge, 1st in line") {
		t.Errorf("CI row = %q", got)
	}
	for n, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 22: "22nd"} {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestMergeQueueRow(t *testing.T) {
	m, be := newModel()
	s := snapshot()
	s.Items[1].MergeQueue = &model.MergeQueue{State: "awaiting_checks", Position: 2, URL: "https://github.com/org/repo/queue/main"}
	s.Items[1].Checks[0].State = model.Passed
	s.Items[1].Checks[0].URL = "u/unit"
	m.SetSnapshot(s)
	keys(m, "2")
	rows := m.View(100, 16)
	want := []string{" Merge queue · 1", "›Q merge queue", " Passed · 1", " √ unit"}
	body := listRows(rows)
	for i, prefix := range want {
		if i >= len(body) || !strings.HasPrefix(body[i], prefix) {
			t.Errorf("list row %d: want prefix %q in\n%s", i, prefix, strings.Join(body, "\n"))
		}
	}
	if !strings.Contains(body[1], "checks running  GitHub") {
		t.Errorf("queue row = %q", body[1])
	}
	if !strings.Contains(rows[4], "1/1 done") {
		t.Errorf("the queue row counts as a check: %q", rows[4])
	}

	// enter opens the queue; b does not set a bell on it.
	keys(m, kEnter, "b")
	if len(be.opened) != 1 || be.opened[0] != "https://github.com/org/repo/queue/main" {
		t.Errorf("opened %v", be.opened)
	}
	if len(be.sent) != 0 || !strings.Contains(m.View(100, 16)[15], "no bell of its own") {
		t.Errorf("b on the queue row sent %+v", be.sent)
	}
	keys(m, "j", kEnter)
	if len(be.opened) != 2 || be.opened[1] != "u/unit" {
		t.Errorf("j then enter opened %v", be.opened)
	}

	// Once the PR leaves the queue, the row goes away.
	s.Items[1].MergeQueue = nil
	m.SetSnapshot(s)
	if got := strings.Join(m.View(100, 16), "\n"); strings.Contains(got, "merge queue") {
		t.Errorf("queue row still shown:\n%s", got)
	}
}

func TestCopyWithoutLink(t *testing.T) {
	m, be := newModel()
	keys(m, "G", "k", "Y") // tide: pending, no URL
	if len(be.copied) != 0 || !strings.Contains(m.View(90, 16)[15], "no link to copy") {
		t.Errorf("copied %v, footer %q", be.copied, m.View(90, 16)[15])
	}
}

func TestLabels(t *testing.T) {
	m, _ := newModel()
	s := snapshot()
	s.Items[0].Labels = []model.Label{{Name: "lgtm", Color: "0e8a16"}, {Name: "do-not-merge/hold", Color: "fbca04"}}
	m.SetSnapshot(s)
	rows := m.View(90, 16)
	if got := headerRow(rows, "labels"); got != " labels:   lgtm · do-not-merge/hold" {
		t.Errorf("label row = %q", got)
	}
	if top := boxTop(rows); !strings.HasPrefix(rows[top-2], " labels:") || !strings.HasPrefix(listRows(rows)[0], " Failed · 1") {
		t.Errorf("rows around the labels:\n%s", strings.Join(rows, "\n"))
	}
	// No labels: no labels row.
	keys(m, "2")
	if got := headerRow(m.View(90, 16), "labels"); got != "" {
		t.Errorf("labels row without labels = %q", got)
	}

	// Labels are bold in their color: kept when readable, lightened just
	// enough when not (darkened on the light flavor); bad colors fall back.
	defer UseTheme(DefaultTheme)
	if got := labelStyle("fbca04"); got != sBold+fg("fbca04") {
		t.Errorf("yellow = %q", got)
	}
	for _, name := range ThemeNames() {
		UseTheme(name)
		for _, c := range []string{"0e8a16", "000000", "b60205", "ffffff", "1d76db"} {
			out := readable(c, theme.base)
			if r := contrast(out, theme.base); r < minContrast {
				t.Errorf("%s: %s became %s, contrast %.1f", name, c, out, r)
			}
		}
	}
	UseTheme("frappe")
	if out := readable("0e8a16", theme.base); out == "0e8a16" || !strings.HasPrefix(labelStyle("0e8a16"), sBold) {
		t.Errorf("dark green kept as is on frappe: %s", out)
	}
	for _, bad := range []string{"", "xyz", "12345", "1234567"} {
		if got := labelStyle(bad); got != sBold+sText {
			t.Errorf("labelStyle(%q) = %q", bad, got)
		}
	}
}

// headerRow returns the header row with the given label ("CI", "alerts"),
// or "".
func headerRow(rows []string, label string) string {
	for _, r := range rows {
		if strings.HasPrefix(r, " "+label+":") {
			return r
		}
	}
	return ""
}

// boxTop returns the row of the list box's top border, or -1.
func boxTop(rows []string) int {
	for i, r := range rows {
		if strings.HasPrefix(r, "┌") {
			return i
		}
	}
	return -1
}

// listRows returns the list's rows: what lies between the column header
// (the line after the box's top border) and the box's bottom border,
// without the side borders.
func listRows(rows []string) []string {
	var out []string
	state := 0 // 0: before the box, 1: on its column header, 2: in its rows
	for _, r := range rows {
		switch {
		case state == 0 && strings.HasPrefix(r, "┌"):
			state = 1
		case state == 1:
			state = 2
		case state == 2 && strings.HasPrefix(r, "└"):
			return out
		case state == 2:
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(r, "│"), "│"))
		}
	}
	return out
}

func TestStyle(t *testing.T) {
	m, _ := newModel()
	m.Color = true
	rows := m.View(90, 16)
	// The active tab is bold, in the base color on the accent.
	if !strings.Contains(rows[1], bgAccent+sBold+sOnAccent+"  1:") {
		t.Errorf("active tab not on the accent: %q", rows[1])
	}
	// The selected row is a bar across the whole list.
	sel := ""
	for _, r := range rows {
		if strings.Contains(r, "e2e-aws-ovn") {
			sel = r
		}
	}
	if !strings.Contains(sel, bgSelect) || strings.Contains(sel, "›") {
		t.Errorf("selected row = %q", sel)
	}
	// Panels: rounded corners with the fancy icons, square with safe ones.
	keys(m, "?")
	model.UseIcons("fancy")
	fancy := strings.Join(m.View(120, 40), "\n")
	model.UseIcons("safe")
	safe := strings.Join(m.View(120, 40), "\n")
	if !strings.Contains(fancy, "╭") || !strings.Contains(safe, "┌") || strings.Contains(safe, "╭") {
		t.Error("panel corners do not follow the icon set")
	}
	if !strings.Contains(fancy, bgOverlay) || !strings.Contains(fancy, bgAccent+sBold+sOnAccent+" Keybindings ") {
		t.Error("panel lacks its background or title chip")
	}
}

func TestNoColorMarkers(t *testing.T) {
	m, _ := newModel() // colors off
	rows := m.View(90, 16)
	if !strings.Contains(rows[1], " [1:") {
		t.Errorf("active tab not marked without colors: %q", rows[1])
	}
	if !strings.HasPrefix(listRows(rows)[1], "›") {
		t.Errorf("selected row not marked without colors: %q", listRows(rows)[1])
	}
	for _, r := range rows {
		if strings.Contains(r, "\x1b[") {
			t.Fatalf("escape sequence without colors: %q", r)
		}
	}
}

func TestFooterHints(t *testing.T) {
	m, _ := newModel()
	// Wide: every hint, in order, saying what each key does.
	wide := m.View(220, 16)[15]
	want := "h/l PR · j/k job · enter open · b bell · v comments · ? help"
	if !strings.Contains(wide, want) {
		t.Errorf("wide footer = %q", wide)
	}
	// The comments list has its own keys.
	keys(m, "v")
	want = "h/l PR · j/k thread · enter open · e expand · r reply · b bell · v checks · ? help"
	if wide := m.View(220, 16)[15]; !strings.Contains(wide, want) {
		t.Errorf("wide footer, comments = %q", wide)
	}
	// 60 columns: the least important hints give way, the line fits beside
	// the connection status, and "? help" stays.
	narrow := m.View(60, 16)[15]
	if !strings.Contains(narrow, "? help") || !strings.Contains(narrow, "enter open") || !strings.Contains(narrow, "connected") {
		t.Errorf("60-column footer = %q", narrow)
	}
	if strings.Contains(narrow, "expand") || strings.Contains(narrow, "…") {
		t.Errorf("60-column footer keeps a low-priority hint or cuts one: %q", narrow)
	}
	keys(m, "v")
	// Very narrow: only "? help" is left.
	if got := hintLine(10, viewChecks).plain(); got != "  ? help" {
		t.Errorf("tiny footer = %q", got)
	}
	// Keys stand out in the accent.
	if l := hintLine(200, viewChecks); l[1].style != sBold+sAccent || l[1].text != "h/l" {
		t.Errorf("key style = %+v", l[1])
	}
}

func TestUnseenMarks(t *testing.T) {
	m, be := newModel()
	s := snapshot()
	s.Items[1].Unseen = true
	m.SetSnapshot(s)
	rows := m.View(120, 16)
	if !strings.Contains(rows[0], "│  1 changed") {
		t.Errorf("title = %q", rows[0])
	}
	if !strings.Contains(rows[1], "#131 sbd-timeout •") || strings.Contains(rows[1], "lease-race •") {
		t.Errorf("tabs = %q", rows[1])
	}

	// Keys on another tab don't mark it seen; switching to it does, once.
	keys(m, "j")
	if len(be.sent) != 0 {
		t.Fatalf("sent %+v while on another tab", be.sent)
	}
	keys(m, "l", "j", "k")
	if len(be.sent) != 1 || be.sent[0].Op != ipc.OpSeen || be.sent[0].ID != "pr:org/repo#131" {
		t.Fatalf("sent %+v, want one seen", be.sent)
	}

	// Once the daemon cleared it, a new change is reported again.
	s.Items[1].Unseen = false
	m.SetSnapshot(s)
	s2 := snapshot()
	s2.Items[1].Unseen = true
	m.SetSnapshot(s2)
	keys(m, "j")
	if len(be.sent) != 2 || be.sent[1].Op != ipc.OpSeen {
		t.Errorf("sent %+v, want a second seen", be.sent)
	}
}

// withComments gives #123 comments read 30 minutes ago: two new ones (in
// the conversation and on a.go:3), an older open thread and a resolved one.
func withComments(s *model.Snapshot) *model.Snapshot {
	c := func(id, author string, ago time.Duration) model.Comment {
		return model.Comment{ID: id, Author: author, Body: "about " + id, CreatedAt: now.Add(-ago), URL: "u/" + id}
	}
	it := &s.Items[0]
	it.CommentsReadAt = now.Add(-30 * time.Minute)
	it.Threads = []model.Thread{
		{ID: model.ConversationID, Total: 9, Comments: []model.Comment{c("c1", "bob", time.Hour), c("c2", "ci-robot", 5*time.Minute)}},
		{ID: "T1", Path: "a.go", Line: 3, Total: 2, Comments: []model.Comment{c("r1", "me", 2*time.Hour), c("r2", "alice", 10*time.Minute)}},
		{ID: "T2", Path: "b.go", Line: 7, Resolved: true, Total: 1, Comments: []model.Comment{c("s1", "alice", 3*time.Hour)}},
		{ID: "T3", Path: "c.go", Line: 1, Outdated: true, Total: 1, Comments: []model.Comment{c("x1", "carol", 2*time.Hour)}},
	}
	return s
}

func TestCommentsList(t *testing.T) {
	m, be := newModel()
	m.SetSnapshot(withComments(snapshot()))
	rows := m.View(100, 20)
	if !strings.Contains(rows[0], "│  2 new comments") || !strings.Contains(rows[1], "#123 lease-race "+bellIcon+" "+commentMark()+" 2") {
		t.Errorf("title = %q, tabs = %q", rows[0], rows[1])
	}
	if c := headerRow(rows, "comments"); c != " comments: 2 new comments · 2 unresolved threads" || !strings.Contains(rows[boxTop(rows)-1], "Comments 2 new") {
		t.Errorf("comments row = %q", c)
	}

	keys(m, "v")
	rows = m.View(100, 20)
	if !strings.Contains(strings.Join(rows, "\n"), "[Comments 2 new]") {
		t.Errorf("list title missing:\n%s", strings.Join(rows, "\n"))
	}
	want := []string{" New · 2", "›• conversation · 9 · 1 new  about c2", " • a.go:3 · 2 · 1 new  about r2", " Open · 1", " c c.go:1 outdated · 1", " Resolved · 1", " √ b.go:7 · 1"}
	body := listRows(rows)
	for i, prefix := range want {
		if i >= len(body) || !strings.HasPrefix(body[i], prefix) {
			t.Errorf("row %d: want prefix %q in\n%s", i, prefix, strings.Join(body, "\n"))
		}
	}
	if f := strings.Fields(body[1]); f[len(f)-3] != "ci-robot" || f[len(f)-2] != "5m" {
		t.Errorf("row 1 = %q", body[1])
	}

	// The key press marked the comments read, once.
	var reads int
	for _, c := range be.sent {
		if c.Op == ipc.OpCommentsRead && c.ID == "pr:org/repo#123" {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("comments_read sent %d times: %+v", reads, be.sent)
	}

	// Keys act on the selected thread.
	keys(m, "j", kEnter, "Y", "b")
	if be.opened[0] != "u/r2" || be.copied[0] != "u/r2" {
		t.Errorf("opened %v, copied %v", be.opened, be.copied)
	}
	last := be.sent[len(be.sent)-1]
	if last.Op != ipc.OpThreadAlerts || last.Thread != "T1" || !*last.On {
		t.Errorf("b sent %+v", last)
	}

	// Once the daemon has them read, they stay new for this visit...
	s := withComments(snapshot())
	s.Items[0].CommentsReadAt = now
	s.Items[0].WatchedThreads = []string{"T1"}
	m.SetSnapshot(s)
	if body := listRows(m.View(100, 20)); !strings.HasPrefix(body[0], " New · 2") || !strings.Contains(body[2], "a.go:3 "+bellIcon) {
		t.Errorf("same visit:\n%s", strings.Join(body, "\n"))
	}
	if rows := m.View(100, 20); strings.Contains(rows[0], commentMark()+" 2") || strings.Contains(rows[1], commentMark()+" 2") {
		t.Errorf("read comments still counted: %q / %q", rows[0], rows[1])
	}
	// ...and not on the next one.
	keys(m, "l", "h")
	body = listRows(m.View(100, 20))
	if !strings.HasPrefix(body[0], " Open · 3") || !strings.HasPrefix(body[1], "›c a.go:3 "+bellIcon+" · 2") || !strings.HasPrefix(body[2], " c conversation · 9") {
		t.Errorf("next visit:\n%s", strings.Join(body, "\n"))
	}

	// Find searches thread names, authors and text.
	keys(m, "/", "c", "a", "r", "o", "l", kEnter)
	if _, th := m.selectedThread(); th == nil || th.ID != "T3" {
		t.Errorf("find selected %+v", th)
	}
	keys(m, "v")
	if !strings.HasPrefix(listRows(m.View(100, 20))[0], " Failed · 1") {
		t.Error("v did not switch back to checks")
	}
}

func TestExpandThread(t *testing.T) {
	m, be := newModel()
	s := withComments(snapshot())
	th := &s.Items[0].Threads[1] // a.go:3
	th.Total = 4                 // two older comments were not fetched
	th.Comments[1].Body = "about r2\n\nsecond paragraph"
	m.SetSnapshot(s)
	keys(m, "e")
	if !strings.Contains(m.View(100, 20)[19], "press v") {
		t.Errorf("e in the checks list: footer %q", m.View(100, 20)[19])
	}

	// Folded, j goes from thread to thread.
	keys(m, "v", "j")
	if _, th, c := m.selectedComment(); th.ID != "T1" || c != nil {
		t.Fatalf("selected %s / %v", th.ID, c)
	}
	keys(m, "e") // expand a.go:3
	body := listRows(m.View(100, 24))
	want := []string{" New · 2", " • conversation", "›• a.go:3 · 4 · 1 new", "   │   a.go:3", "   │   ⋯ 2 earlier comments on GitHub",
		"   │   me · 2h00m ago", "   │     about r1", "   │ • alice · 10m ago", "   │     about r2", "   │", "   │     second paragraph", " Open · 1"}
	for i, prefix := range want {
		if i >= len(body) || !strings.HasPrefix(body[i], prefix) {
			t.Errorf("row %d: want prefix %q in\n%s", i, prefix, strings.Join(body, "\n"))
		}
	}

	// Expanded, j steps through its comments, then on to the next thread.
	keys(m, "j", "j")
	if _, _, c := m.selectedComment(); c == nil || c.ID != "r2" {
		t.Fatalf("two j from the thread selected %+v", c)
	}
	if body := listRows(m.View(100, 24)); !strings.HasPrefix(body[7], "   › • alice") || strings.HasPrefix(body[2], "›") {
		t.Errorf("selected comment not marked:\n%s", strings.Join(body, "\n"))
	}
	keys(m, kEnter, "Y", "b")
	if be.opened[len(be.opened)-1] != "u/r2" || be.copied[len(be.copied)-1] != "u/r2" {
		t.Errorf("opened %v, copied %v", be.opened, be.copied)
	}
	if last := be.sent[len(be.sent)-1]; last.Op != ipc.OpThreadAlerts || last.Thread != "T1" {
		t.Errorf("b on a comment sent %+v", last)
	}
	keys(m, "j")
	if _, th, c := m.selectedComment(); th.ID != "T3" || c != nil {
		t.Errorf("j past the last comment selected %s / %v", th.ID, c)
	}

	// e on a comment folds its thread and selects it.
	keys(m, "k", "e")
	if _, th, c := m.selectedComment(); th.ID != "T1" || c != nil {
		t.Errorf("after folding, selected %s / %v", th.ID, c)
	}
	if body := listRows(m.View(100, 24)); strings.Contains(strings.Join(body, "\n"), "│") {
		t.Errorf("folded thread still expanded:\n%s", strings.Join(body, "\n"))
	}
}

func TestWrap(t *testing.T) {
	got := wrap("one two three four\n\n\n  indented words here\nabcdefghijklmnop", 10)
	want := []string{"one two", "three four", "", "  indented", "  words", "  here", "abcdefghij", "klmnop"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrap = %q", got)
	}
}

func TestOpensOnComments(t *testing.T) {
	m := NewModel(&fakeBackend{})
	m.now = func() time.Time { return now }
	m.Color = false
	m.SetConnected(true)
	m.SetSnapshot(withComments(snapshot()))
	rows := m.View(100, 20)
	if !strings.Contains(rows[boxTop(rows)-1], "[Comments 2 new]") || !strings.HasPrefix(listRows(rows)[0], " New · 2") {
		t.Errorf("first screen:\n%s", strings.Join(rows, "\n"))
	}
}

func TestThemes(t *testing.T) {
	defer UseTheme(DefaultTheme)
	if err := UseTheme("dracula"); err == nil {
		t.Error("unknown theme accepted")
	}
	// Every color that carries content reads well on the dark flavors'
	// base (WCAG's 4.5:1); Overlay 1 and Surface 2 are only decoration.
	for _, name := range []string{"frappe", "macchiato", "mocha"} {
		f := themes[name]
		for role, c := range map[string]string{"text": f.text, "subtext1": f.subtext1, "subtext0": f.subtext0,
			"blue": f.blue, "sapphire": f.sapphire, "red": f.red, "green": f.green, "yellow": f.yellow,
			"peach": f.peach, "mauve": f.mauve} {
			if r := contrast(c, f.base); r < 4.5 {
				t.Errorf("%s %s: contrast %.1f", name, role, r)
			}
		}
		if r := contrast(f.text, f.surface1); r < 4.5 {
			t.Errorf("%s: selected row contrast %.1f", name, r)
		}
	}
	if err := UseTheme("mocha"); err != nil || sText != fg("cdd6f4") || bgSelect != bg("45475a") {
		t.Errorf("mocha: %v %q", err, sText)
	}
}

func TestLifecycleChip(t *testing.T) {
	m, _ := newModel()
	rows := m.View(100, 16)
	if !strings.HasPrefix(rows[3], " [OPEN] Fix lease renewal race  org/repo#123") {
		t.Errorf("open header = %q", rows[3])
	}
	// A long title gives way, the reference does not.
	s := snapshot()
	s.Items[0].Title = strings.Repeat("very long title ", 10)
	m.SetSnapshot(s)
	if got := m.View(80, 16)[3]; !strings.Contains(got, "…  org/repo#123") {
		t.Errorf("long title header = %q", got)
	}
	keys(m, "3")
	rows = m.View(100, 16)
	if !strings.HasPrefix(rows[3], " [MERGED] Docs  osac/osac#58") || strings.Contains(rows[4], "merged") {
		t.Errorf("merged header = %q / %q", rows[3], rows[4])
	}
	// Not fetched yet: no state to show.
	s = snapshot()
	s.Items[2].HeadSHA = ""
	m.SetSnapshot(s)
	if rows := m.View(100, 16); strings.Contains(rows[3], "[") {
		t.Errorf("unfetched header = %q", rows[3])
	}
	// The chips' text (the base color) reads well on them.
	for _, name := range []string{"frappe", "macchiato", "mocha"} {
		f := themes[name]
		for _, c := range []string{f.green, f.mauve, f.red} {
			if r := contrast(f.base, c); r < 4.5 {
				t.Errorf("%s chip %s: contrast %.1f", name, c, r)
			}
		}
	}
}

func TestReply(t *testing.T) {
	m, be := newModel()
	m.SetSnapshot(withComments(snapshot()))
	keys(m, "r")
	if len(be.edited) != 0 || !strings.Contains(m.View(100, 20)[19], "press v") {
		t.Fatalf("r in the checks list: edited %v, footer %q", be.edited, m.View(100, 20)[19])
	}

	// On the conversation: a new comment, asked before posting.
	be.edits = []string{"Looks good\nthanks\n"}
	keys(m, "v", "r")
	if m.mode != modeConfirm || m.confirmMsg != "Comment on org/repo#123 (2 lines)?" {
		t.Fatalf("mode %v, confirm %q", m.mode, m.confirmMsg)
	}
	sent := len(be.sent)
	keys(m, "n") // declined: kept as a draft
	if len(be.sent) != sent {
		t.Fatalf("declined reply sent: %+v", be.sent[sent:])
	}
	be.edits = []string{"Looks good, thanks"}
	keys(m, "r")
	if be.edited[len(be.edited)-1] != "Looks good\nthanks" {
		t.Errorf("draft not reopened: %q", be.edited)
	}
	keys(m, "y")
	last := be.sent[len(be.sent)-1]
	if last.Op != ipc.OpReply || last.Thread != model.ConversationID || last.Body != "Looks good, thanks" {
		t.Fatalf("sent %+v", last)
	}
	m.Done(last, "commented on org/repo#123", nil) // posted: the draft goes
	be.edits = []string{""}
	keys(m, "r")
	if be.edited[len(be.edited)-1] != "" || m.mode != modeNormal || !strings.Contains(m.View(100, 20)[19], "nothing written") {
		t.Errorf("after posting: editor got %q, mode %v", be.edited[len(be.edited)-1], m.mode)
	}

	// On a code thread: a reply in it; a failed post keeps the draft.
	be.edits = []string{"done"}
	keys(m, "j", "r")
	if m.confirmMsg != "Reply on a.go:3 (1 line)?" {
		t.Fatalf("confirm %q", m.confirmMsg)
	}
	keys(m, "y")
	last = be.sent[len(be.sent)-1]
	m.Done(last, "", errors.New("github: 403 forbidden"))
	be.edits = []string{"done"}
	keys(m, "r")
	if be.edited[len(be.edited)-1] != "done" {
		t.Errorf("failed reply's draft lost: %q", be.edited)
	}
	keys(m, kEsc)

	// The editor failing says so.
	be.edits = nil
	keys(m, "r")
	if m.mode != modeNormal || !strings.Contains(m.View(100, 20)[19], "no editor") {
		t.Errorf("editor error not shown: %q", m.View(100, 20)[19])
	}
}

func TestResolve(t *testing.T) {
	m, be := newModel()
	m.SetSnapshot(withComments(snapshot()))
	keys(m, "v", "R") // the conversation
	if m.mode != modeNormal || !strings.Contains(m.View(100, 20)[19], "cannot be resolved") {
		t.Fatalf("R on the conversation: %q", m.View(100, 20)[19])
	}
	keys(m, "j", "R", "y") // a.go:3, unresolved
	last := be.sent[len(be.sent)-1]
	if last.Op != ipc.OpResolve || last.Thread != "T1" || !*last.On {
		t.Errorf("resolve sent %+v", last)
	}
	keys(m, "G", "R") // b.go:7, resolved
	if m.confirmMsg != "Unresolve b.go:7?" {
		t.Errorf("confirm %q", m.confirmMsg)
	}
	keys(m, "y")
	if last := be.sent[len(be.sent)-1]; last.Thread != "T2" || *last.On {
		t.Errorf("unresolve sent %+v", last)
	}
	// In the checks list, R still re-runs failed CI.
	keys(m, "v", "R")
	if m.confirmMsg != "Re-run the failed CI of org/repo#123?" {
		t.Errorf("R in the checks list: %q", m.confirmMsg)
	}
}

func TestScrollComment(t *testing.T) {
	m, _ := newModel()
	s := withComments(snapshot())
	var long []string
	for i := 1; i <= 30; i++ {
		long = append(long, fmt.Sprintf("line %d", i))
	}
	s.Items[0].Threads[1].Comments[0].Body = strings.Join(long, "\n") // r1, by me
	m.SetSnapshot(s)
	keys(m, "v", "j", "e", "j") // a.go:3 expanded, its first comment selected
	view := func() []string { return listRows(m.View(100, 20)) }
	body := view()
	n := len(body)
	if !strings.HasPrefix(body[n-1], "   │     ↓ ") || !strings.HasSuffix(strings.TrimRight(body[n-1], " "), "more lines · J") {
		t.Fatalf("no more-lines marker:\n%s", strings.Join(body, "\n"))
	}

	// J scrolls the comment; the lines above give way to a marker.
	keys(m, "J", "J", "J")
	body = view()
	if !strings.HasPrefix(body[0], "   │     ↑ 3 lines above · K") || !strings.HasPrefix(body[1], "   │     line 3") {
		t.Errorf("after 3 J:\n%s", strings.Join(body, "\n"))
	}
	if _, _, c := m.selectedComment(); c == nil || c.ID != "r1" {
		t.Errorf("J moved the selection to %+v", c)
	}
	// Scrolling stops at the end of the comment.
	for i := 0; i < 50; i++ {
		keys(m, "J")
	}
	body = view()
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "line 30") || strings.Contains(joined, "more line") {
		t.Errorf("at the end:\n%s", joined)
	}
	keys(m, "K")
	// The marker covers a line too: two are hidden below.
	if !strings.Contains(strings.Join(view(), "\n"), "2 more lines · J") {
		t.Errorf("after K:\n%s", strings.Join(view(), "\n"))
	}
	// j jumps to the next comment, which starts unscrolled.
	keys(m, "j")
	if _, _, c := m.selectedComment(); c == nil || c.ID != "r2" || m.cscroll != 0 {
		t.Errorf("j selected %+v, scroll %d", c, m.cscroll)
	}
	// J on a thread row, or in the checks list, does nothing.
	keys(m, "g", "g", "J")
	if m.cscroll != 0 {
		t.Errorf("J on a thread scrolled to %d", m.cscroll)
	}
}

func TestLegend(t *testing.T) {
	m, _ := newModel()
	keys(m, "?")
	if screen := strings.Join(m.View(100, 30), "\n"); !strings.Contains(screen, "[Keybindings]") || strings.Contains(screen, "Marks") {
		t.Fatalf("first page:\n%s", screen)
	}
	keys(m, kTab)
	screen := strings.Join(m.View(100, 30), "\n")
	if !strings.Contains(screen, "[Legend]") || m.mode != modeHelp {
		t.Fatalf("tab did not show the legend:\n%s", screen)
	}
	// Every icon and mark, with its meaning, in full.
	for _, sec := range m.legend() {
		for _, e := range sec.keys {
			if !strings.Contains(screen, e.key) || !strings.Contains(screen, e.desc) {
				t.Errorf("legend lacks %q %q:\n%s", e.key, e.desc, screen)
			}
		}
	}
	keys(m, kTab)
	if !strings.Contains(strings.Join(m.View(100, 30), "\n"), "Navigation") {
		t.Error("tab did not go back to the keys")
	}
	keys(m, "x")
	if m.mode != modeNormal {
		t.Error("any other key should close the panel")
	}
	keys(m, "?")
	if m.helpPage != 0 {
		t.Error("? should open on the keys")
	}
}

func TestCIRow(t *testing.T) {
	m, _ := newModel()
	ci := func(checks ...model.Check) string {
		s := snapshot()
		s.Items[0].Checks = checks
		m.SetSnapshot(s)
		row := headerRow(m.View(120, 20), "CI")
		row, _, _ = strings.Cut(row, " · on a1b2c3d")
		return row
	}
	req := func(c model.Check) model.Check { c.Required = true; return c }
	for _, tc := range []struct {
		checks []model.Check
		want   string
	}{
		{[]model.Check{{Name: "a", State: model.Failed}, {Name: "b", State: model.Passed}}, " CI:       2/2 done · 1 failing"},
		{[]model.Check{req(model.Check{Name: "a", State: model.Passed}), {Name: "b", State: model.Failed}}, " CI:       2/2 done · 1 failing, none required"},
		{[]model.Check{req(model.Check{Name: "a", State: model.Failed}), {Name: "b", State: model.Failed}, {Name: "c", State: model.Pending}}, " CI:       2/3 done · 2 failing (1 required) · 1 running"},
		{nil, " CI:       no checks reported yet"},
	} {
		if got := ci(tc.checks...); got != tc.want {
			t.Errorf("CI row = %q, want %q", got, tc.want)
		}
	}
}

func TestShortScreenKeepsFooter(t *testing.T) {
	m, _ := newModel()
	s := withComments(snapshot())
	s.Items[0].Labels = []model.Label{{Name: "lgtm", Color: "0e8a16"}}
	m.SetSnapshot(s)
	rows := m.View(100, 11)
	if !strings.Contains(rows[10], "connected") || headerRow(rows, "CI") == "" || headerRow(rows, "labels") != "" {
		t.Errorf("short screen:\n%s", strings.Join(rows, "\n"))
	}
	if top := boxTop(rows); top < 0 || len(listRows(rows)) == 0 {
		t.Errorf("no list on a short screen:\n%s", strings.Join(rows, "\n"))
	}
}

func TestListTabs(t *testing.T) {
	m, _ := newModel()
	m.SetSnapshot(withComments(snapshot()))
	tabs := func() string { rows := m.View(100, 24); return rows[boxTop(rows)-1] }
	// Checks shown: its tab is bracketed (highlighted with colors), and
	// each tab carries its own count.
	if got := tabs(); got != "  Comments 2 new   [Checks 1 failing]   v switches" {
		t.Errorf("checks tabs = %q", got)
	}
	keys(m, "v")
	if got := tabs(); got != " [Comments 2 new]   Checks 1 failing    v switches" {
		t.Errorf("comments tabs = %q", got)
	}
	// Nothing failing: the running jobs; nothing new: no count.
	s := snapshot()
	s.Items[0].Checks[1].State = model.Passed
	m.SetSnapshot(s)
	if got := tabs(); got != " [Comments]   Checks 2 running    v switches" {
		t.Errorf("tabs = %q", got)
	}
	row := boxTop(m.View(100, 24)) - 1
	m.Color = true
	if got := m.View(100, 24)[row]; !strings.Contains(got, bgAccent+sBold+sOnAccent+" Comments") {
		t.Errorf("active tab not highlighted: %q", got)
	}
}

func TestThreadNames(t *testing.T) {
	ts := []model.Thread{
		{ID: model.ConversationID},
		{ID: "a", Path: "docs/x/prd.md", Line: 3},
		{ID: "b", Path: "docs/y/prd.md", Line: 7},
		{ID: "c", Path: "pkg/main.go", Line: 1},
		{ID: "d", Path: "pkg/main.go", Line: 9},
		{ID: "e", Path: "README.md"},
	}
	got := threadNames(ts)
	want := map[string]string{model.ConversationID: "conversation", "a": "x/prd.md:3", "b": "y/prd.md:7", "c": "main.go:1", "d": "main.go:9", "e": "README.md"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
}

func TestCheckRowTags(t *testing.T) {
	m, _ := newModel()
	s := snapshot()
	s.Items[0].Checks[0].Source = "GitHub Advanced Security"
	s.Items[0].WatchedChecks = []string{"e2e-metal-ipi"}
	m.SetSnapshot(s)
	body := listRows(m.View(100, 20))
	// opt follows the name (after its bell); long sources are shortened.
	if !strings.HasPrefix(body[3], " * e2e-metal-ipi "+bellIcon+" opt ") {
		t.Errorf("optional job row = %q", body[3])
	}
	if !strings.Contains(strings.Join(body, "\n"), "GH Security") {
		t.Errorf("source not shortened:\n%s", strings.Join(body, "\n"))
	}
	// Times are neutral: only the icon carries the state's color.
	m.Color = true
	for _, r := range m.View(100, 20) {
		if strings.Contains(r, "running 23m") && strings.Contains(r, sYellow+padLeft("running 23m", timeW)) {
			t.Errorf("running time in the state color: %q", r)
		}
	}
}
