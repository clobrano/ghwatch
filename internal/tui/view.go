package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/clobrano/ghwatch/internal/model"
)

func stateStyle(s model.State) string {
	switch s {
	case model.Failed:
		return sRed
	case model.Passed:
		return sGreen
	case model.Running:
		return sYellow
	case model.Merged:
		return sMagenta
	case model.Pending, model.Skipped, model.Cancelled, model.Closed:
		return sDim
	}
	return ""
}

func icon(s model.State) seg { return seg{s.Icon(), stateStyle(s)} }

// View renders the screen as width-wide rows, exactly height of them.
func (m *Model) View(width, height int) []string {
	rows := m.lines(width, height)
	out := make([]string, height)
	for i := range out {
		if i < len(rows) {
			out[i] = rows[i].render(width, m.Color)
		}
	}
	return out
}

func (m *Model) lines(w, h int) []line {
	if w < 20 || h < 7 {
		return []line{{{"ghwatch: terminal too small", ""}}}
	}
	rule := line{{strings.Repeat("─", w), sDim}}
	out := []line{m.tabBar(w), rule}
	out = append(out, m.header(w)...)
	out = append(out, rule)
	body := h - len(out) - 2
	var b []line
	switch m.mode {
	case modeFind:
		b = m.findBody(w, body)
	case modeEvents:
		b = m.eventsBody()
	case modeHelp:
		b = helpBody(w, body)
	default:
		b = m.checksBody(w, body)
	}
	for i := 0; i < body; i++ {
		if i < len(b) {
			out = append(out, b[i])
		} else {
			out = append(out, line{})
		}
	}
	return append(out, rule, m.footer(w))
}

// tabLabels builds the label of every tab.
func (m *Model) tabLabels() []line {
	items := m.items()
	counts := map[string]int{}
	common, best := "", 0
	for _, it := range items {
		counts[it.Repo]++
		if counts[it.Repo] > best {
			common, best = it.Repo, counts[it.Repo]
		}
	}
	labels := make([]line, len(items))
	for i, it := range items {
		st := model.ItemState(it)
		if it.HeadSHA == "" && it.Error == "" {
			st = model.Pending
		}
		name := "#" + strconv.Itoa(it.Number)
		if it.Number == 0 {
			name = it.ID
		} else if it.Repo != common {
			_, short, _ := strings.Cut(it.Repo, "/")
			name = short + name
		}
		if s := slug(it); s != "" {
			name += " " + s
		}
		l := line{{" ", ""}, icon(st), {" " + name, ""}}
		if it.Alerts {
			l = append(l, seg{" ⍾", sCyan})
		}
		l = append(l, seg{" ", ""})
		if i == m.activeIdx {
			for j := range l {
				l[j].style = sReverse + sBold + l[j].style
			}
		}
		labels[i] = l
	}
	return labels
}

// slug is a short name for a tab, from the branch or else the title.
func slug(it model.Item) string {
	src := it.Branch
	if i := strings.LastIndexByte(src, '/'); i >= 0 {
		src = src[i+1:]
	}
	if src == "" || src == "main" || src == "master" {
		src = it.Title
	}
	words := strings.FieldsFunc(strings.ToLower(src), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	// Skip ticket numbers and leading noise words.
	var keep []string
	for _, w := range words {
		if len(keep) == 0 && (isNumeric(w) || w == "fix" || w == "feat" || w == "chore" || w == "wip") {
			continue
		}
		keep = append(keep, w)
		if len(keep) == 2 {
			break
		}
	}
	return truncate(strings.Join(keep, "-"), 14)
}

func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func (m *Model) tabBar(w int) line {
	labels := m.tabLabels()
	if len(labels) == 0 {
		return line{{" ghwatch", sBold}, {" · no watched items", sDim}}
	}
	sep := seg{"│", sDim}
	width := func(from, to int) int { // cells for labels[from..to]
		n := 0
		for i := from; i <= to; i++ {
			n += labels[i].width() + 1
		}
		return n - 1
	}
	start := 0
	for start < m.activeIdx && width(start, m.activeIdx)+2 > w {
		start++
	}
	var out line
	used := 0
	if start > 0 {
		out = append(out, seg{"‹", sDim})
		used++
	}
	for i := start; i < len(labels); i++ {
		lw := labels[i].width()
		if i > start {
			lw++
		}
		reserve := 0
		if i < len(labels)-1 {
			reserve = 1
		}
		if used+lw+reserve > w && i > start {
			out = append(out, seg{"›", sDim})
			break
		}
		if i > start {
			out = append(out, sep)
		}
		out = append(out, labels[i]...)
		used += lw
	}
	return out
}

func (m *Model) header(w int) []line {
	it := m.current()
	if it == nil {
		return []line{{{" Nothing watched yet", sBold}}, {{" press a to add a PR, or run: ghwatch add <PR URL>", sDim}}}
	}
	l1 := spread(
		line{{" " + ref(*it), sBold}, {"  " + it.Title, ""}},
		line{{"@" + it.Author + " ", sDim}}, w)
	if it.Author == "" {
		l1 = line{{" " + ref(*it), sBold}, {"  " + it.Title, ""}}
	}
	dot := seg{" · ", sDim}
	var l2 line
	add := func(s seg) {
		if len(l2) > 0 {
			l2 = append(l2, dot)
		}
		l2 = append(l2, s)
	}
	switch {
	case it.HeadSHA == "" && it.Error != "":
	case it.HeadSHA == "":
		add(seg{"waiting for the first poll…", sDim})
	default:
		add(seg{"head " + short(it.HeadSHA), ""})
		if !it.PushedAt.IsZero() {
			add(seg{"pushed " + human(m.now().Sub(it.PushedAt)) + " ago", ""})
		}
		done, failed := model.Progress(it.Checks)
		add(seg{fmt.Sprintf("%d/%d done", done, len(it.Checks)), ""})
		if failed > 0 {
			add(seg{fmt.Sprintf("%d failing", failed), sRed})
		}
		if it.Lifecycle.Finished() {
			add(seg{string(it.Lifecycle), stateStyle(model.ItemState(*it)) + sBold})
		}
	}
	if it.Alerts {
		add(seg{"alerts on", sCyan})
	}
	if it.Error != "" {
		add(seg{"⚠ " + it.Error, sRed})
	}
	if len(l2) > 0 {
		l2 = append(line{{" ", ""}}, l2...)
	}
	return []line{l1, l2}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (m *Model) checkTime(c model.Check) string {
	switch c.State {
	case model.Running:
		if c.StartedAt != nil {
			return "running " + human(m.now().Sub(*c.StartedAt))
		}
		return "running"
	case model.Pending:
		return "pending"
	}
	if c.StartedAt != nil && c.CompletedAt != nil {
		return human(c.CompletedAt.Sub(*c.StartedAt))
	}
	switch c.State {
	case model.Skipped:
		return "skipped"
	case model.Cancelled:
		return "cancelled"
	}
	return ""
}

func (m *Model) checksBody(w, rows int) []line {
	it := m.current()
	if it == nil {
		return nil
	}
	if len(it.Checks) == 0 {
		if it.HeadSHA == "" {
			return nil
		}
		return []line{{{" no checks reported on this head yet", sDim}}}
	}
	anyRequired := false
	for _, c := range it.Checks {
		anyRequired = anyRequired || c.Required
	}
	checks := grouped(it.Checks)
	sel := m.selIndex(it.ID, checks)

	// One header row per non-empty group, then its checks.
	type row struct {
		check int // index in checks, or -1 for a group header
		group int
		count int
	}
	var list []row
	selRow := 0
	for i, c := range checks {
		g := checkGroup(c.State)
		if i == 0 || checkGroup(checks[i-1].State) != g {
			list = append(list, row{check: -1, group: g})
		}
		if i == sel {
			selRow = len(list)
		}
		list = append(list, row{check: i, group: g})
	}
	for i := range list {
		if list[i].check < 0 {
			for j := i + 1; j < len(list) && list[j].check >= 0; j++ {
				list[i].count++
			}
		}
	}

	// Scroll so the selected check is visible, with its group header when
	// it is the first of its group.
	top := m.top[it.ID]
	first := selRow
	if first > 0 && list[first-1].check < 0 {
		first--
	}
	if first < top {
		top = first
	}
	if selRow >= top+rows {
		top = selRow - rows + 1
	}
	top = max(min(top, len(list)-rows), 0)
	m.top[it.ID] = top

	const timeW, srcW = 14, 8
	optW := 0
	if anyRequired {
		optW = 4
	}
	nameW := max(w-3-1-timeW-2-srcW-optW, 8)
	groupStyle := [...]string{sRed, sYellow, sGreen, sDim}
	var out []line
	for _, r := range list[top:] {
		if len(out) >= rows {
			break
		}
		if r.check < 0 {
			out = append(out, line{{" " + groupNames[r.group], groupStyle[r.group] + sBold}, {" · " + strconv.Itoa(r.count), sDim}})
			continue
		}
		c := checks[r.check]
		marker, nameStyle := seg{" ", ""}, ""
		if r.check == sel {
			marker, nameStyle = seg{"▸", sCyan + sBold}, sBold
		}
		l := line{marker, icon(c.State), {" " + padRight(c.Name, nameW), nameStyle},
			{" " + padLeft(m.checkTime(c), timeW), stateStyle(c.State)},
			{"  " + padRight(c.Source, srcW), sDim}}
		if anyRequired && !c.Required {
			l = append(l, seg{" opt", sDim})
		}
		out = append(out, l)
	}
	return out
}

func (m *Model) findBody(w, rows int) []line {
	matches := m.findMatches()
	if len(matches) == 0 {
		return []line{{{" no match", sDim}}}
	}
	sel := min(m.findSel, len(matches)-1)
	m.findSel = sel
	start := max(sel-rows+1, 0)
	var out []line
	for n, idx := range matches[start:] {
		if len(out) >= rows {
			break
		}
		it := m.items()[idx]
		marker, style := seg{" ", ""}, ""
		if start+n == sel {
			marker, style = seg{"▸", sCyan + sBold}, sBold
		}
		out = append(out, line{marker, icon(model.ItemState(it)), {" " + ref(it), style}, {"  " + it.Title, sDim}})
	}
	return out
}

func (m *Model) eventsBody() []line {
	out := []line{{{" Notify on these events, for items with alerts on (n):", sBold}}, {}}
	var st model.Settings
	if m.snap != nil {
		st = m.snap.Settings
	}
	box := func(on bool) string {
		if on {
			return "[x] "
		}
		return "[ ] "
	}
	row := func(i int, text string) line {
		if i == m.evSel {
			return line{{" ▸ ", sCyan + sBold}, {text, sBold}}
		}
		return line{{"   ", ""}, {text, ""}}
	}
	for i, e := range model.EventTypes {
		out = append(out, row(i, box(st.Events[e])+e.Label()))
	}
	out = append(out, line{}, row(len(model.EventTypes), box(st.Mute)+"global mute (keeps per-item settings)"))
	return append(out, line{}, line{{" j/k move · space toggle · esc close", sDim}})
}

var helpRows = [][2]string{
	{"h / l, gT / gt", "previous / next tab"},
	{"1 – 9", "jump to tab N"},
	{"/", "find a PR by number or title"},
	{"j / k, gg / G", "move between checks"},
	{"enter", "open the selected check's job page"},
	{"o", "open the PR page"},
	{"y", "copy the selected check's URL"},
	{"n", "toggle notifications for this item"},
	{"N", "notification settings: event types, global mute"},
	{"a", "add a PR"},
	{"d", "unwatch this PR (in every client)"},
	{"r", "ask the daemon to poll now"},
	{"R", "re-run failed CI (/retest, Actions re-run)"},
	{"?", "this help"},
	{"q", "quit this TUI (the daemon keeps running)"},
}

// helpBody lists the keys, in as many columns (of at least 50 cells) as
// the width allows when they do not fit the height.
func helpBody(w, rows int) []line {
	cols := 1
	if avail := rows - 1; avail < len(helpRows) {
		cols = max(min(w/50, (len(helpRows)+avail-1)/max(avail, 1)), 1)
	}
	per := (len(helpRows) + cols - 1) / cols
	colW := w / cols
	out := []line{{{" Keys · any key closes", sBold}}}
	for r := 0; r < per; r++ {
		var l line
		for c := 0; c < cols; c++ {
			i := c*per + r
			if i >= len(helpRows) {
				break
			}
			l = append(l, seg{"  " + padRight(helpRows[i][0], 16), sCyan}, seg{padRight(helpRows[i][1], colW-18), ""})
		}
		out = append(out, l)
	}
	return out
}

func (m *Model) footer(w int) line {
	var right line
	if m.snap != nil && m.snap.Settings.Mute {
		right = append(right, seg{"muted · ", sDim})
	}
	switch {
	case !m.connected:
		right = append(right, seg{"disconnected", sRed + sBold})
		if m.snap != nil {
			right = append(right, seg{" · stale", sRed})
		}
	case m.snap != nil && m.snap.Stale:
		right = append(right, seg{"connected · stale", sYellow})
	default:
		right = append(right, seg{"connected", sGreen})
	}
	right = append(right, seg{" ", ""})

	var left line
	switch {
	case m.mode == modeAdd:
		left = line{{" Add PR (URL or owner/repo#N): ", sBold}, {string(m.input) + "▏", ""}}
	case m.mode == modeFind:
		left = line{{" /", sBold}, {string(m.input) + "▏", ""}, {"   ↑/↓ choose · enter jump · esc cancel", sDim}}
	case m.mode == modeConfirm:
		left = line{{" " + m.confirmMsg + " ", sBold}, {"[y/N]", sYellow}}
	case m.flash != "" && m.now().Before(m.flashUntil):
		style := sYellow
		if m.flashErr {
			style = sRed
		}
		left = line{{" " + m.flash, style}}
	case m.snap != nil && m.snap.Stale && m.snap.Error != "":
		left = line{{" ⚠ " + m.snap.Error, sYellow}}
	default:
		left = line{{" h/l tab · j/k check · enter job · o PR · n alerts · N events · ?", sDim}}
	}
	return spread(left, right, w)
}
