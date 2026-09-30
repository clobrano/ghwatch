// Package tui is the tabbed terminal UI: one tab per watched item, each
// listing every check. All shared state lives in the daemon; the TUI only
// keeps per-client UI state (active tab, selection, scroll).
package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/model"
)

// Backend performs the side effects the TUI asks for.
type Backend interface {
	// Send delivers a command to the daemon; the outcome comes back
	// through Model.Result.
	Send(cmd ipc.Command)
	// Open opens a URL in the browser.
	Open(url string) error
	// Copy puts text on the clipboard.
	Copy(text string) error
}

type mode int

const (
	modeNormal mode = iota
	modeAdd
	modeFind
	modeEvents
	modeHelp
	modeConfirm
)

// Model is the TUI state. It is not safe for concurrent use: the event
// loop owns it.
type Model struct {
	backend Backend
	now     func() time.Time
	// Color enables ANSI styles.
	Color bool

	snap      *model.Snapshot
	connected bool

	active    string               // ID of the active tab
	activeIdx int                  // its index, to stay in place if it disappears
	sel       map[string]selection // selected check per item
	top       map[string]int       // first visible check per item

	mode       mode
	input      []rune
	findSel    int
	evSel      int
	confirmMsg string
	confirmCmd ipc.Command
	pendingG   bool

	flash      string
	flashErr   bool
	flashUntil time.Time

	quit bool
}

// NewModel returns an empty model.
func NewModel(b Backend) *Model {
	return &Model{backend: b, now: time.Now, sel: map[string]selection{}, top: map[string]int{}, Color: true}
}

// Quit reports whether the user asked to leave.
func (m *Model) Quit() bool { return m.quit }

func (m *Model) items() []model.Item {
	if m.snap == nil {
		return nil
	}
	return m.snap.Items
}

// SetSnapshot replaces the displayed state.
func (m *Model) SetSnapshot(s *model.Snapshot) {
	m.snap = s
	items := m.items()
	if len(items) == 0 {
		m.active, m.activeIdx = "", 0
		return
	}
	for i, it := range items {
		if it.ID == m.active {
			m.activeIdx = i
			return
		}
	}
	// The active item is gone (or none yet): stay at the same position.
	m.activeIdx = min(max(m.activeIdx, 0), len(items)-1)
	m.active = items[m.activeIdx].ID
}

// SetConnected records the daemon connection state.
func (m *Model) SetConnected(ok bool) { m.connected = ok }

// Result shows the outcome of a command.
func (m *Model) Result(info string, err error) {
	if err != nil {
		m.setFlash(err.Error(), true)
	} else if info != "" {
		m.setFlash(info, false)
	}
}

func (m *Model) setFlash(s string, isErr bool) {
	m.flash, m.flashErr, m.flashUntil = s, isErr, m.now().Add(4*time.Second)
}

func (m *Model) current() *model.Item {
	items := m.items()
	if len(items) == 0 {
		return nil
	}
	return &items[m.activeIdx]
}

func (m *Model) setActive(i int) {
	items := m.items()
	if len(items) == 0 {
		return
	}
	i = min(max(i, 0), len(items)-1)
	m.activeIdx, m.active = i, items[i].ID
}

// selection remembers the selected check by identity, so it follows the
// check when it moves to another group. idx is the fallback when the check
// is gone (for example after a new push).
type selection struct {
	key string
	idx int
}

// Check groups, in display order.
const (
	groupQueue = iota
	groupFailed
	groupRunning
	groupPassed
	groupCancelled
)

var groupNames = [...]string{"Merge queue", "Failed", "Running", "Passed", "Cancelled"}

// checkGroup puts a check in its display group. Pending checks have not
// finished, so they go with the running ones; skipped ones need nothing,
// so they go with the passed ones.
func checkGroup(s model.State) int {
	switch s {
	case model.Queued:
		return groupQueue
	case model.Failed:
		return groupFailed
	case model.Running, model.Pending:
		return groupRunning
	case model.Cancelled:
		return groupCancelled
	}
	return groupPassed
}

// grouped returns the checks in display order: failed, running, passed,
// then cancelled. Within a group, checks keep the daemon's first-seen order.
func grouped(checks []model.Check) []model.Check {
	out := append([]model.Check(nil), checks...)
	sort.SliceStable(out, func(i, j int) bool { return checkGroup(out[i].State) < checkGroup(out[j].State) })
	return out
}

// queueRow is the list row standing for an item's merge queue entry: it
// can be selected and opened like a check, but exists only in the view.
func queueRow(q *model.MergeQueue) model.Check {
	return model.Check{Name: "merge queue", Source: "GitHub", State: model.Queued, URL: q.URL}
}

// listed returns the rows of an item's check list: its merge queue entry,
// when it is queued, then its checks grouped.
func listed(it *model.Item) []model.Check {
	out := grouped(it.Checks)
	if q := it.MergeQueue; q != nil {
		out = append([]model.Check{queueRow(q)}, out...)
	}
	return out
}

// selIndex returns the index of the selected check in checks (display order).
func (m *Model) selIndex(id string, checks []model.Check) int {
	s := m.sel[id]
	if s.key != "" {
		for i, c := range checks {
			if c.Key() == s.key {
				return i
			}
		}
	}
	return min(max(s.idx, 0), len(checks)-1)
}

func (m *Model) selected() (*model.Item, *model.Check) {
	it := m.current()
	if it == nil {
		return nil, nil
	}
	checks := listed(it)
	if len(checks) == 0 {
		return it, nil
	}
	return it, &checks[m.selIndex(it.ID, checks)]
}

func (m *Model) moveCheck(delta int) {
	it := m.current()
	if it == nil {
		return
	}
	checks := listed(it)
	if len(checks) == 0 {
		return
	}
	i := min(max(m.selIndex(it.ID, checks)+delta, 0), len(checks)-1)
	m.sel[it.ID] = selection{key: checks[i].Key(), idx: i}
}

func (m *Model) send(cmd ipc.Command) {
	if !m.connected {
		m.setFlash("disconnected from the daemon; try again when connected", true)
		return
	}
	m.backend.Send(cmd)
}

// Key handles one key press.
func (m *Model) Key(k string) {
	switch m.mode {
	case modeAdd:
		m.keyInput(k, func(s string) {
			if s = strings.TrimSpace(s); s != "" {
				m.send(ipc.Command{Op: ipc.OpAdd, ID: s})
				m.setFlash("adding "+s+"…", false)
			}
		})
	case modeFind:
		m.keyFind(k)
	case modeEvents:
		m.keyEvents(k)
	case modeHelp:
		m.mode = modeNormal
	case modeConfirm:
		m.mode = modeNormal
		if k == "y" || k == "Y" {
			m.send(m.confirmCmd)
		}
	default:
		m.keyNormal(k)
	}
}

func (m *Model) keyNormal(k string) {
	if m.pendingG {
		m.pendingG = false
		switch k {
		case "t":
			m.setActive((m.activeIdx + 1) % max(len(m.items()), 1))
		case "T":
			m.setActive((m.activeIdx - 1 + len(m.items())) % max(len(m.items()), 1))
		case "g":
			m.moveCheck(-1 << 20)
		}
		return
	}
	switch k {
	case "q", kCtrlC:
		m.quit = true
	case "h", kLeft, kShiftTab:
		m.setActive(m.activeIdx - 1)
	case "l", kRight, kTab:
		m.setActive(m.activeIdx + 1)
	case "g":
		m.pendingG = true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.setActive(int(k[0] - '1'))
	case "j", kDown:
		m.moveCheck(1)
	case "k", kUp:
		m.moveCheck(-1)
	case kPgDown:
		m.moveCheck(10)
	case kPgUp:
		m.moveCheck(-10)
	case "G", kEnd:
		m.moveCheck(1 << 20)
	case kHome:
		m.moveCheck(-1 << 20)
	case kEnter:
		if _, c := m.selected(); c != nil {
			m.open(c.URL)
		}
	case "o":
		if it := m.current(); it != nil {
			m.open(it.URL)
		}
	case "y":
		if _, c := m.selected(); c != nil && c.URL != "" {
			if err := m.backend.Copy(c.URL); err != nil {
				m.setFlash(err.Error(), true)
			} else {
				m.setFlash("copied "+c.URL, false)
			}
		}
	case "n":
		if it := m.current(); it != nil {
			on := !it.Alerts
			m.send(ipc.Command{Op: ipc.OpAlerts, ID: it.ID, On: &on})
		}
	case "b":
		if it, c := m.selected(); c != nil && c.State == model.Queued {
			m.setFlash("the merge queue has no bell of its own; n covers the merge", true)
		} else if c != nil {
			on := !it.Watching(c.Name)
			m.send(ipc.Command{Op: ipc.OpCheckAlerts, ID: it.ID, Check: c.Name, On: &on})
		}
	case "N":
		m.mode, m.evSel = modeEvents, 0
	case "a":
		m.mode, m.input = modeAdd, nil
	case "d":
		if it := m.current(); it != nil {
			m.confirm("Unwatch "+ref(*it)+" in every client?", ipc.Command{Op: ipc.OpUnwatch, ID: it.ID})
		}
	case "r":
		m.send(ipc.Command{Op: ipc.OpPoll})
	case "R":
		if it := m.current(); it != nil {
			if _, failed := model.Progress(it.Checks); failed == 0 {
				m.setFlash("no failed checks to re-run", true)
			} else {
				m.confirm("Re-run the failed CI of "+ref(*it)+"?", ipc.Command{Op: ipc.OpRetest, ID: it.ID})
			}
		}
	case "/":
		m.mode, m.input, m.findSel = modeFind, nil, 0
	case "?":
		m.mode = modeHelp
	}
}

func (m *Model) confirm(msg string, cmd ipc.Command) {
	m.mode, m.confirmMsg, m.confirmCmd = modeConfirm, msg, cmd
}

func (m *Model) open(url string) {
	if url == "" {
		m.setFlash("no link for this check", true)
		return
	}
	if err := m.backend.Open(url); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash("opening "+url, false)
}

// keyInput edits the input line; submit is called on enter.
func (m *Model) keyInput(k string, submit func(string)) bool {
	switch k {
	case kEsc, kCtrlC:
		m.mode = modeNormal
	case kEnter:
		m.mode = modeNormal
		submit(string(m.input))
	case kBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case kCtrlU:
		m.input = nil
	case kCtrlW:
		s := strings.TrimRight(string(m.input), " ")
		if i := strings.LastIndexAny(s, " /#"); i >= 0 {
			m.input = []rune(s[:i+1])
		} else {
			m.input = nil
		}
	default:
		if utf8.RuneCountInString(k) != 1 {
			return false
		}
		m.input = append(m.input, []rune(k)...)
	}
	return true
}

func (m *Model) keyFind(k string) {
	switch k {
	case kDown, kCtrlN, kTab:
		m.findSel++
		return
	case kUp, kCtrlP, kShiftTab:
		m.findSel = max(m.findSel-1, 0)
		return
	}
	before := string(m.input)
	m.keyInput(k, func(string) {
		it := m.current()
		checks, matches := m.findMatches()
		if it == nil || len(matches) == 0 {
			return
		}
		i := matches[min(m.findSel, len(matches)-1)]
		m.sel[it.ID] = selection{key: checks[i].Key(), idx: i}
	})
	if string(m.input) != before {
		m.findSel = 0
	}
}

// findMatches returns the current tab's job list (as displayed) and the
// indexes of the jobs whose name matches the find query, ignoring case,
// best first.
func (m *Model) findMatches() ([]model.Check, []int) {
	it := m.current()
	if it == nil {
		return nil, nil
	}
	checks := listed(it)
	q := strings.ToLower(string(m.input))
	type hit struct{ idx, score int }
	var hits []hit
	for i, c := range checks {
		if s, ok := fuzzy(q, strings.ToLower(c.Name)); ok {
			hits = append(hits, hit{i, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score < hits[b].score })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.idx
	}
	return checks, out
}

// fuzzy reports whether q is a subsequence of s; lower scores are
// tighter matches.
func fuzzy(q, s string) (int, bool) {
	if q == "" {
		return 0, true
	}
	if i := strings.Index(s, q); i >= 0 {
		return i, true // contiguous matches rank first
	}
	score, last, qi := 1000, -1, 0
	qr := []rune(q)
	for i, r := range []rune(s) {
		if qi < len(qr) && r == qr[qi] {
			if last >= 0 {
				score += i - last - 1
			}
			last = i
			qi++
		}
	}
	return score, qi == len(qr)
}

func (m *Model) keyEvents(k string) {
	rows := len(model.EventTypes) + 1
	switch k {
	case "j", kDown, kTab:
		m.evSel = (m.evSel + 1) % rows
	case "k", kUp, kShiftTab:
		m.evSel = (m.evSel - 1 + rows) % rows
	case " ", "x", kEnter:
		if m.snap == nil {
			return
		}
		if m.evSel == len(model.EventTypes) {
			on := !m.snap.Settings.Mute
			m.send(ipc.Command{Op: ipc.OpMute, On: &on})
			return
		}
		e := model.EventTypes[m.evSel]
		m.send(ipc.Command{Op: ipc.OpEvents, Events: map[model.EventType]bool{e: !m.snap.Settings.Events[e]}})
	case kEsc, "q", "N":
		m.mode = modeNormal
	}
}

// ref is the short reference of an item, e.g. org/repo#123.
func ref(it model.Item) string {
	if it.Repo != "" && it.Number > 0 {
		return it.Repo + "#" + strconv.Itoa(it.Number)
	}
	return it.ID
}
