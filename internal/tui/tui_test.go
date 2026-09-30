package tui

import (
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
		"✗ #123 lease-race ⍾",
		"● #131 sbd-timeout",
		"⮌ osac#58 docs",
		"org/repo#123  Fix lease renewal race",
		"@clobrano",
		"head a1b2c3d · pushed 52m ago · 2/4 done · 1 failing · alerts on",
		"running 23m",
		"pending",
		" opt",
		"connected",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	rows := m.View(90, 16)
	if len(rows) != 16 {
		t.Errorf("View returned %d rows", len(rows))
	}
	if !strings.HasPrefix(rows[5], "▸✓ lint") || !strings.Contains(rows[5], "2m") {
		t.Errorf("first check row = %q", rows[5])
	}
}

func TestTabOverflowKeepsActiveVisible(t *testing.T) {
	m, _ := newModel()
	keys(m, "3")
	bar := m.View(30, 10)[0]
	if !strings.Contains(bar, "osac#58") || !strings.HasPrefix(bar, "‹") {
		t.Errorf("tab bar = %q", bar)
	}
	keys(m, "1")
	bar = m.View(30, 10)[0]
	if !strings.Contains(bar, "#123") || !strings.HasSuffix(strings.TrimRight(bar, " "), "›") {
		t.Errorf("tab bar = %q", bar)
	}
}

func TestNavigationAndActions(t *testing.T) {
	m, be := newModel()
	keys(m, "j", "j", kEnter, "o", "y")
	if !reflect.DeepEqual(be.opened, []string{"u/metal", "https://github.com/org/repo/pull/123"}) {
		t.Errorf("opened = %v", be.opened)
	}
	if !reflect.DeepEqual(be.copied, []string{"u/metal"}) {
		t.Errorf("copied = %v", be.copied)
	}
	keys(m, "G")
	if _, c := m.selected(); c.Name != "tide" {
		t.Errorf("G selected %s", c.Name)
	}
	keys(m, "g", "g")
	if _, c := m.selected(); c.Name != "lint" {
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
	if _, c := m.selected(); c.Name != "e2e-aws-ovn" {
		t.Errorf("selection not kept per tab: %s", c.Name)
	}

	keys(m, "n")
	if len(be.sent) != 1 || be.sent[0].Op != ipc.OpAlerts || *be.sent[0].On != false {
		t.Errorf("n sent %+v", be.sent)
	}
	keys(m, "r")
	if be.sent[1].Op != ipc.OpPoll {
		t.Errorf("r sent %+v", be.sent[1])
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
	m, _ := newModel()
	keys(m, "/", "s", "b", "d", kEnter)
	if m.active != "pr:org/repo#131" {
		t.Errorf("find sbd: active = %s", m.active)
	}
	keys(m, "/", "5", "8", kEnter)
	if m.active != "pr:osac/osac#58" {
		t.Errorf("find 58: active = %s", m.active)
	}
	keys(m, "/", "z", "z", "z", kEnter)
	if m.active != "pr:osac/osac#58" {
		t.Errorf("no match moved the tab")
	}
}

func TestEventsPanel(t *testing.T) {
	m, be := newModel()
	keys(m, "N")
	screen := strings.Join(m.View(80, 20), "\n")
	if !strings.Contains(screen, "▸ [x] check failed") || !strings.Contains(screen, "[ ] global mute") {
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
	keys(m, "r")
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
	m, _ := newModel()
	keys(m, "?")
	screen := strings.Join(m.View(150, 16), "\n")
	for _, r := range helpRows {
		if !strings.Contains(screen, r[1]) {
			t.Errorf("help lacks %q:\n%s", r[1], screen)
		}
	}
	keys(m, "x")
	if m.mode != modeNormal {
		t.Error("any key should close help")
	}
}
