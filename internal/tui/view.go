package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/clobrano/ghwatch/internal/model"
)

// appTitle is the app name as shown in the title bar.
const appTitle = "GHWATCH"

// appTagline follows the name in the title bar.
const appTagline = "GitHub PR watcher"

// bellIcon marks alerts: the Nerd Fonts codicon bell (nf-cod-bell).
const bellIcon = "\ueaa2"

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
	case model.Queued:
		return sBlue
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
	if w < 20 || h < 8 {
		return []line{{{"ghwatch: terminal too small", ""}}}
	}
	rule := line{{strings.Repeat("─", w), sDim}}
	out := []line{m.titleBar(w), m.tabBar(w), rule}
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

// titleBar is the top line: the app name, how many PRs are watched and
// in which state, how many have alerts, and when GitHub was last polled.
func (m *Model) titleBar(w int) line {
	left := line{{" " + appTitle, sBold + sCyan}, {"  " + appTagline, sDim}}
	if m.snap == nil {
		return spread(left, line{{"waiting for the daemon… ", sDim}}, w)
	}
	items := m.items()
	prs := "PRs"
	if len(items) == 1 {
		prs = "PR"
	}
	left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%d %s", len(items), prs), ""})
	counts := map[model.State]int{}
	alerts := 0
	for _, it := range items {
		st := model.ItemState(it)
		if st == model.Pending {
			st = model.Running
		}
		if st == model.Closed {
			st = model.Merged
		}
		counts[st]++
		if it.Alerts || len(it.WatchedChecks) > 0 {
			alerts++
		}
	}
	for _, st := range []model.State{model.Failed, model.Running, model.Passed, model.Queued, model.Merged} {
		if counts[st] > 0 {
			left = append(left, seg{" ", ""}, seg{fmt.Sprintf("%s%d", st.Icon(), counts[st]), stateStyle(st)})
		}
	}
	if alerts > 0 {
		left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%s %d", bellIcon, alerts), sCyan})
	}
	if m.snap.Settings.Mute {
		left = append(left, seg{" · ", sDim}, seg{"muted", sYellow})
	}

	var right line
	switch {
	case m.snap.PolledAt.IsZero():
		right = line{{"not polled yet ", sDim}}
	case m.snap.Stale:
		right = line{{"last poll " + human(m.now().Sub(m.snap.PolledAt)) + " ago ", sYellow}}
	default:
		right = line{{"polled " + human(m.now().Sub(m.snap.PolledAt)) + " ago ", sDim}}
	}
	return spread(left, right, w)
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
		if it.Alerts || len(it.WatchedChecks) > 0 {
			l = append(l, seg{" " + bellIcon, sCyan})
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
		return line{{" no watched items", sDim}}
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
		// The merge queue comes first: it is what matters most while queued.
		if q := it.MergeQueue; q != nil {
			add(m.queueSeg(q))
		}
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
	if n := len(it.WatchedChecks); n > 0 {
		jobs := "jobs"
		if n == 1 {
			jobs = "job"
		}
		add(seg{fmt.Sprintf("%s %d %s", bellIcon, n, jobs), sCyan})
	}
	if it.Error != "" {
		add(seg{"⚠ " + it.Error, sRed})
	}
	if len(l2) > 0 {
		l2 = append(line{{" ", ""}}, l2...)
	}
	return []line{l1, l2}
}

// queueSeg describes a merge queue entry, e.g. "queued for merge, 2nd in
// line, 12m ago, ~8m left".
func (m *Model) queueSeg(q *model.MergeQueue) seg {
	what, style := "queued for merge", stateStyle(model.Queued)+sBold
	switch q.State {
	case "awaiting_checks":
		what = "in merge queue, checks running"
	case "mergeable":
		what = "in merge queue, ready to merge"
	case "unmergeable":
		what, style = "in merge queue, unmergeable", sRed+sBold
	case "locked":
		what = "in merge queue, queue locked"
	}
	if q.Position > 0 {
		what += ", " + ordinal(q.Position) + " in line"
	}
	if !q.EnqueuedAt.IsZero() {
		what += ", " + human(m.now().Sub(q.EnqueuedAt)) + " ago"
	}
	if q.ETASeconds > 0 {
		what += ", ~" + human(time.Duration(q.ETASeconds)*time.Second) + " left"
	}
	return seg{what, style}
}

// queueStatus is the short state of a merge queue entry for the list row.
func queueStatus(q *model.MergeQueue) (string, string) {
	switch q.State {
	case "awaiting_checks":
		return "checks running", stateStyle(model.Queued)
	case "mergeable":
		return "ready to merge", stateStyle(model.Queued)
	case "unmergeable":
		return "unmergeable", sRed
	case "locked":
		return "queue locked", stateStyle(model.Queued)
	}
	if q.Position > 0 {
		return ordinal(q.Position) + " in line", stateStyle(model.Queued)
	}
	return "queued", stateStyle(model.Queued)
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
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
	checks := listed(it)
	if len(checks) == 0 {
		if it.HeadSHA == "" {
			return nil
		}
		return []line{{{" no checks reported on this head yet", sDim}}}
	}
	anyRequired := false
	for _, c := range it.Checks {
		anyRequired = anyRequired || c.Required
	}
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

	groupStyle := [...]string{sBlue, sRed, sYellow, sGreen, sDim}
	var out []line
	for _, r := range list[top:] {
		if len(out) >= rows {
			break
		}
		if r.check < 0 {
			out = append(out, line{{" " + groupNames[r.group], groupStyle[r.group] + sBold}, {" · " + strconv.Itoa(r.count), sDim}})
			continue
		}
		out = append(out, m.checkRow(it, checks[r.check], r.check == sel, w, anyRequired))
	}
	return out
}

// checkRow renders one row of the check list, w cells wide.
func (m *Model) checkRow(it *model.Item, c model.Check, selected bool, w int, anyRequired bool) line {
	const timeW, srcW = 14, 8
	optW := 0
	if anyRequired {
		optW = 4
	}
	nameW := max(w-3-1-timeW-2-srcW-optW, 8)
	marker, nameStyle := seg{" ", ""}, ""
	if selected {
		marker, nameStyle = seg{"▸", sCyan + sBold}, sBold
	}
	// The name cell ends with a bell when the job has its own alerts.
	name := line{{" " + padRight(c.Name, nameW), nameStyle}}
	if it.Watching(c.Name) {
		n := truncate(c.Name, nameW-2)
		name = line{{" " + n, nameStyle}, {" " + bellIcon, sCyan}, {strings.Repeat(" ", max(nameW-strWidth(n)-2, 0)), ""}}
	}
	when, whenStyle := m.checkTime(c), stateStyle(c.State)
	if c.State == model.Queued {
		when, whenStyle = queueStatus(it.MergeQueue)
	}
	l := append(line{marker, icon(c.State)}, name...)
	l = append(l, seg{" " + padLeft(when, timeW), whenStyle},
		seg{"  " + padRight(c.Source, srcW), sDim})
	if anyRequired && !c.Required && c.State != model.Queued {
		l = append(l, seg{" opt", sDim})
	}
	return l
}

// findBody lists the jobs of the current tab that match the find query.
func (m *Model) findBody(w, rows int) []line {
	it := m.current()
	checks, matches := m.findMatches()
	if len(matches) == 0 {
		return []line{{{" no matching job", sDim}}}
	}
	anyRequired := false
	for _, c := range it.Checks {
		anyRequired = anyRequired || c.Required
	}
	sel := min(m.findSel, len(matches)-1)
	m.findSel = sel
	out := []line{{{fmt.Sprintf(" %d of %d jobs match", len(matches), len(checks)), sDim}}}
	start := max(sel-(rows-1)+1, 0)
	for n, idx := range matches[start:] {
		if len(out) >= rows {
			break
		}
		out = append(out, m.checkRow(it, checks[idx], start+n == sel, w, anyRequired))
	}
	return out
}

func (m *Model) eventsBody() []line {
	out := []line{{{" Notify on these events, for items with alerts on (n):", sBold}},
		{{" (a job with its own bell (b) notifies on every change of its state)", sDim}}, {}}
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
	{"/", "find a job in this tab (ignores case)"},
	{"j / k, gg / G", "move between checks"},
	{"enter", "open the selected check's job page"},
	{"o", "open the PR page"},
	{"y / Y", "copy the PR's / the selected job's URL"},
	{"n", "toggle notifications for this item"},
	{"b", "toggle notifications for the selected job"},
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
		left = line{{" /", sBold}, {string(m.input) + "▏", ""}, {"   ↑/↓ choose · enter select · esc cancel", sDim}}
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
		left = line{{" h/l tab · j/k check · enter job · o PR · n/b alerts · N events · ?", sDim}}
	}
	return spread(left, right, w)
}
