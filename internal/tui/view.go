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

// unseenMark flags PRs that changed since a user last looked at them.
// The bullet is in every common monospace font.
const unseenMark = "•"

// bellIcon marks alerts: the Nerd Fonts codicon bell (nf-cod-bell).
const bellIcon = "\ueaa2"

// bellStyle is the style for an item's bells, or dimmed while it is muted.
func bellStyle(it *model.Item, style string) string {
	if it.Muted {
		return sDim
	}
	return style
}

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
		return sSecondary
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
	if w < 20 || h < 10 {
		return []line{{{"ghwatch: terminal too small", ""}}}
	}
	switch m.mode {
	case modeHelp:
		return overlay(w, h, "Keybindings", m.helpContent(w, h))
	case modeEvents:
		return overlay(w, h, "Notifications", m.eventsContent())
	}
	out := []line{m.titleBar(w), m.tabBar(w), {{strings.Repeat("─", w), sBorder}}}
	out = append(out, m.header(w)...)
	// The job list sits in a box: border, column header, separator, rows.
	rows := h - len(out) - 1 - 4
	var b []line
	switch {
	case m.mode == modeFind && m.view == viewComments:
		b = m.findThreadsBody(w-2, rows)
	case m.mode == modeFind:
		b = m.findBody(w-2, rows)
	case m.view == viewComments:
		b = m.commentsBody(w-2, rows)
	default:
		b = m.checksBody(w-2, rows)
	}
	content := []line{m.columnHeader(w - 2), {{strings.Repeat("─", w-2), sBorder}}}
	for i := 0; i < rows; i++ {
		if i < len(b) {
			content = append(content, b[i])
		} else {
			content = append(content, line{})
		}
	}
	list := box(content, w, sBorder, "", false)
	list[0] = m.listTitle(w)
	out = append(out, list...)
	return append(out, m.footer(w))
}

// listTitle is the list box's top border, naming the two lists a PR has,
// comments first, with the one shown highlighted: v switches between them.
func (m *Model) listTitle(w int) line {
	name := func(text string, on bool) seg {
		if on {
			if !m.Color {
				return seg{"[" + text + "]", ""}
			}
			return seg{" " + text + " ", bgAccent + sBold + sOnAccent}
		}
		return seg{" " + text + " ", sMuted}
	}
	comments := "Comments"
	if it := m.current(); it != nil {
		if k := m.newShown(it); k > 0 {
			comments += " " + strconv.Itoa(k) + " new"
		}
	}
	l := line{{"┌─", sBorder}, name(comments, m.view == viewComments), {"─", sBorder}, name("Checks", m.view == viewChecks)}
	l = append(l, seg{" v switches ", sDim})
	if n := w - 1 - l.width(); n > 0 {
		l = append(l, seg{strings.Repeat("─", n), sBorder})
	}
	return append(cut(l, w-1), seg{"┐", sBorder})
}

// newShown counts the comments of it shown as new in this visit.
func (m *Model) newShown(it *model.Item) int {
	readAt := m.readMark(it)
	n := 0
	for _, t := range it.Threads {
		n += t.NewSince(readAt)
	}
	return n
}

// titleBar is the top line: the app name, how many PRs are watched and
// in which state, how many have alerts, and when GitHub was last polled.
func (m *Model) titleBar(w int) line {
	left := line{{" " + appTitle, sBold + sAccent}, {"  " + appTagline, sDim}}
	if m.snap == nil {
		return spread(left, line{{"waiting for the daemon… ", sSecondary}}, w)
	}
	items := m.items()
	prs := "PRs"
	if len(items) == 1 {
		prs = "PR"
	}
	left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%d %s", len(items), prs), sMuted})
	counts := map[model.State]int{}
	alerts, unseen, comments := 0, 0, 0
	for _, it := range items {
		comments += it.NewComments()
		st := model.ItemState(it)
		if st == model.Pending {
			st = model.Running
		}
		if st == model.Closed {
			st = model.Merged
		}
		counts[st]++
		if it.HasAlerts() && !it.Muted {
			alerts++
		}
		if it.Unseen {
			unseen++
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
	if unseen > 0 {
		left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%s%d new", unseenMark, unseen), sBold + sNew})
	}
	if comments > 0 {
		left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%s %d", commentMark(), comments), sBold + sNew})
	}
	if m.snap.Settings.Mute {
		left = append(left, seg{" · ", sDim}, seg{"muted", sYellow})
	}

	var right line
	switch {
	case m.snap.PolledAt.IsZero():
		right = line{{"not polled yet ", sSecondary}}
	case m.snap.Stale:
		right = line{{"last poll " + human(m.now().Sub(m.snap.PolledAt)) + " ago ", sYellow}}
	default:
		right = line{{"polled " + human(m.now().Sub(m.snap.PolledAt)) + " ago ", sSecondary}}
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
		l := line{{"  " + strconv.Itoa(i+1) + ":", sMuted}, icon(st), {" " + name, sMuted}}
		if it.HasAlerts() {
			l = append(l, seg{" " + bellIcon, bellStyle(&it, sAccent)})
		}
		if it.Unseen {
			l = append(l, seg{" " + unseenMark, sBold + sNew})
		}
		if n := it.NewComments(); n > 0 {
			l = append(l, seg{fmt.Sprintf(" %s %d", commentMark(), n), sBold + sNew})
		}
		l = append(l, seg{"  ", ""})
		if i == m.activeIdx {
			// Like jira-tabbed-tui: bold white on the accent.
			for j := range l {
				l[j].style = bgAccent + sBold + sOnAccent
			}
			if !m.Color {
				l[0].text = " [" + l[0].text[2:]
				l[len(l)-1].text = "] "
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
		return line{{" no watched items", sSecondary}}
	}
	width := func(from, to int) int { // cells for labels[from..to]
		n := 0
		for i := from; i <= to; i++ {
			n += labels[i].width()
		}
		return n
	}
	start := 0
	for start < m.activeIdx && width(start, m.activeIdx)+2 > w {
		start++
	}
	var out line
	used := 0
	if start > 0 {
		out = append(out, seg{"‹", sSecondary})
		used++
	}
	for i := start; i < len(labels); i++ {
		lw := labels[i].width()
		reserve := 0
		if i < len(labels)-1 {
			reserve = 1
		}
		if used+lw+reserve > w && i > start {
			out = append(out, seg{"›", sSecondary})
			break
		}
		out = append(out, labels[i]...)
		used += lw
	}
	return out
}

func (m *Model) header(w int) []line {
	it := m.current()
	if it == nil {
		return []line{{{" Nothing watched yet", sBold}}, {{" press a to add a PR, or run: ghwatch add <PR URL>", sSecondary}}}
	}
	title := line{{" " + ref(*it), sBold + sAccent}, {"  " + it.Title, sBold + sStrong}}
	l1 := spread(title, line{{"@" + it.Author + " ", sSecondary}}, w)
	if it.Author == "" {
		l1 = title
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
		add(seg{"waiting for the first poll…", sSecondary})
	default:
		// The merge queue comes first: it is what matters most while queued.
		if q := it.MergeQueue; q != nil {
			add(m.queueSeg(q))
		}
		add(seg{"head " + short(it.HeadSHA), sMuted})
		if !it.PushedAt.IsZero() {
			add(seg{"pushed " + human(m.now().Sub(it.PushedAt)) + " ago", sMuted})
		}
		done, failed := model.Progress(it.Checks)
		add(seg{fmt.Sprintf("%d/%d done", done, len(it.Checks)), sMuted})
		if failed > 0 {
			add(seg{fmt.Sprintf("%d failing", failed), sRed})
		}
		if it.Lifecycle.Finished() {
			add(seg{string(it.Lifecycle), stateStyle(model.ItemState(*it)) + sBold})
		}

	}
	if it.Muted {
		// The muted alerts are restored as they were: no need to detail them.
		add(seg{bellIcon + " muted (m restores)", sMuted})
	} else if it.Alerts {
		add(seg{"alerts on", sAccent})
	}
	if n := len(it.WatchedChecks); n > 0 && !it.Muted {
		add(seg{bellIcon + " " + plural(n, "job"), sAccent})
	}
	if n := len(it.WatchedThreads); n > 0 && !it.Muted {
		add(seg{bellIcon + " " + plural(n, "thread"), sAccent})
	}
	// Comments come last: new ones are counted in the list's title.
	open := 0
	for _, t := range it.Threads {
		if t.ID != model.ConversationID && !t.Resolved {
			open++
		}
	}
	if open > 0 {
		add(seg{plural(open, "unresolved thread"), sMuted})
	}
	if it.Error != "" {
		add(seg{"! " + it.Error, sRed})
	}
	if len(l2) > 0 {
		l2 = append(line{{" ", ""}}, l2...)
	}
	out := []line{l1, l2}
	if len(it.Labels) > 0 {
		out = append(out, labelLine(it.Labels))
	}
	return out
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

// labelLine lists the labels, each in bold in its GitHub color.
func labelLine(labels []model.Label) line {
	l := line{{" labels: ", sSecondary}}
	for i, lb := range labels {
		if i > 0 {
			l = append(l, seg{" · ", sDim})
		}
		l = append(l, seg{lb.Name, labelStyle(lb.Color)})
	}
	return l
}

// labelStyle is bold text in the label's color, made lighter (or darker
// on a light theme) just enough to read on the theme's background.
func labelStyle(hex string) string {
	if !validHex(hex) {
		return sBold + sText
	}
	return sBold + fg(readable(hex, theme.base))
}

// plural is "1 job" or "2 jobs".
func plural(n int, what string) string {
	if n != 1 {
		what += "s"
	}
	return strconv.Itoa(n) + " " + what
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
	checks := m.listed(it)
	if len(checks) == 0 {
		if it.HeadSHA == "" {
			return nil
		}
		return []line{{{" no checks reported on this head yet", sSecondary}}}
	}
	anyRequired := false
	for _, c := range it.Checks {
		anyRequired = anyRequired || c.Required
	}
	sel := m.selIndex(it.ID, checks)
	groups := make([]int, len(checks))
	for i, c := range checks {
		groups[i] = checkGroup(c.State)
	}
	groupStyle := [...]string{sBlue, sRed, sYellow, sGreen, sSecondary}
	return m.groupedRows(it.ID, groups, sel, 0, -1, rows,
		func(g, n int) line {
			return line{{" " + groupNames[g], groupStyle[g] + sBold}, {" · " + strconv.Itoa(n), sSecondary}}
		},
		func(i int) []line { return []line{m.checkRow(it, checks[i], i == sel, w, anyRequired)} })
}

// groupedRows lays out a list whose entries are sorted by group: a header
// row per non-empty group, then its entries, scrolled so the selected
// entry is visible (with its group header when it is the first of its
// group). An entry is its row and, when expanded, lines below it. Lines
// from to to (-1: the last) of the selected entry are kept in view, as
// far as they fit, from the first. topKey keeps the scroll position of
// this list between frames.
func (m *Model) groupedRows(topKey string, groups []int, sel, from, to, rows int, header func(group, count int) line, entry func(i int) []line) []line {
	type row struct {
		entry int // index of the entry, or -1 for a group header
		line  int // line of the entry
		group int
		count int
	}
	var list []row
	var lines [][]line
	selRow, selEnd := 0, 0
	for i, g := range groups {
		if i == 0 || groups[i-1] != g {
			list = append(list, row{entry: -1, group: g})
		}
		e := entry(i)
		lines = append(lines, e)
		if i == sel {
			last := len(e) - 1
			if to >= 0 {
				last = min(to, last)
			}
			selRow, selEnd = len(list)+min(from, last), len(list)+last
		}
		for k := range e {
			list = append(list, row{entry: i, line: k, group: g})
		}
	}
	for i := range list {
		if list[i].entry < 0 {
			for j := i + 1; j < len(list) && list[j].entry >= 0; j++ {
				if list[j].line == 0 {
					list[i].count++
				}
			}
		}
	}
	top := m.top[topKey]
	first := selRow
	if first > 0 && list[first-1].entry < 0 && list[first].line == 0 {
		first--
	}
	if first < top {
		top = first
	}
	if selEnd >= top+rows {
		top = min(selEnd-rows+1, selRow)
	}
	top = max(min(top, len(list)-rows), 0)
	m.top[topKey] = top

	var out []line
	for _, r := range list[top:] {
		if len(out) >= rows {
			break
		}
		if r.entry < 0 {
			out = append(out, header(r.group, r.count))
			continue
		}
		out = append(out, lines[r.entry][r.line])
	}
	return out
}

// Job list columns: marker, icon, name, time, source, "opt".
const timeW, srcW = 14, 8

// nameWidth is the width of the name column in a w-wide list.
func nameWidth(w int, anyRequired bool) int {
	optW := 0
	if anyRequired {
		optW = 4
	}
	return max(w-3-1-timeW-2-srcW-optW, 8)
}

// columnHeader is the job list's header row, like jira-tabbed-tui's.
func (m *Model) columnHeader(w int) line {
	if m.view == viewComments {
		return line{{"   " + padRight("Thread", threadNameWidth(w)) + " " + padLeft("Last", timeW) + "  " + padRight("Author", authorW), sBold + sText}}
	}
	anyRequired := false
	if it := m.current(); it != nil {
		for _, c := range it.Checks {
			anyRequired = anyRequired || c.Required
		}
	}
	return line{{"   " + padRight("Check", nameWidth(w, anyRequired)) + " " + padLeft("Time", timeW) + "  " + padRight("Source", srcW), sBold + sText}}
}

// checkRow renders one row of the check list, w cells wide. The selected
// row is a full-width bar, or carries a › marker without colors.
func (m *Model) checkRow(it *model.Item, c model.Check, selected bool, w int, anyRequired bool) line {
	nameW := nameWidth(w, anyRequired)
	marker, nameStyle := seg{" ", ""}, sText
	if selected {
		marker, nameStyle = seg{" ", ""}, sBold+sStrong
		if !m.Color {
			marker = seg{"›", ""}
		}
	}
	// The name cell ends with a bell when the job has its own alerts.
	name := line{{" " + padRight(c.Name, nameW), nameStyle}}
	if it.Watching(c.Name) {
		n := truncate(c.Name, nameW-2)
		name = line{{" " + n, nameStyle}, {" " + bellIcon, bellStyle(it, sCyan)}, {strings.Repeat(" ", max(nameW-strWidth(n)-2, 0)), ""}}
	}
	when, whenStyle := m.checkTime(c), stateStyle(c.State)
	if c.State == model.Queued {
		when, whenStyle = queueStatus(it.MergeQueue)
	}
	l := append(line{marker, icon(c.State)}, name...)
	l = append(l, seg{" " + padLeft(when, timeW), whenStyle},
		seg{"  " + padRight(c.Source, srcW), sSecondary})
	if anyRequired && !c.Required && c.State != model.Queued {
		l = append(l, seg{" opt", sSecondary})
	}
	if selected && m.Color {
		return withStyle(fill(l, w, ""), bgSelect+sBold)
	}
	return l
}

// findBody lists the jobs of the current tab that match the find query.
func (m *Model) findBody(w, rows int) []line {
	it := m.current()
	checks, matches := m.findMatches()
	if len(matches) == 0 {
		return []line{{{" no matching job", sSecondary}}}
	}
	anyRequired := false
	for _, c := range it.Checks {
		anyRequired = anyRequired || c.Required
	}
	sel := min(m.findSel, len(matches)-1)
	m.findSel = sel
	out := []line{{{fmt.Sprintf(" %d of %d jobs match", len(matches), len(checks)), sSecondary}}}
	start := max(sel-(rows-1)+1, 0)
	for n, idx := range matches[start:] {
		if len(out) >= rows {
			break
		}
		out = append(out, m.checkRow(it, checks[idx], start+n == sel, w, anyRequired))
	}
	return out
}

// overlay draws a panel centered on an empty screen, like
// jira-tabbed-tui's keybindings panel: an accent border, a dark background
// and a title chip.
func overlay(w, h int, title string, content []line) []line {
	body := []line{{{" " + title + " ", bgAccent + sBold + sOnAccent}}, {}}
	body = append(body, content...)
	inner := 0
	for _, l := range body {
		inner = max(inner, l.width())
	}
	bw := min(inner+6, w)
	var padded []line
	padded = append(padded, line{})
	for _, l := range body {
		padded = append(padded, append(line{{"  ", ""}}, l...))
	}
	padded = append(padded, line{})
	for i := range padded {
		padded[i] = withStyle(fill(padded[i], bw-2, ""), bgOverlay)
	}
	panel := box(padded, bw, sPanelBorder, "", rounded())
	if len(panel) > h {
		panel = append(panel[:h-1], panel[len(panel)-1])
	}
	top, left := (h-len(panel))/2, (w-bw)/2
	out := make([]line, top, h)
	for _, l := range panel {
		out = append(out, append(line{{strings.Repeat(" ", left), ""}}, l...))
	}
	return out
}

// eventsContent is the notification settings panel.
func (m *Model) eventsContent() []line {
	var st model.Settings
	if m.snap != nil {
		st = m.snap.Settings
	}
	check := func(on bool) string {
		if on {
			return "[x] "
		}
		return "[ ] "
	}
	const rowW = 54
	row := func(i int, text string) line {
		if i == m.evSel {
			l := line{{"› ", sAccent + sBold}, {text, sBold + sStrong}}
			if m.Color {
				return withStyle(fill(l, rowW, ""), bgSoft)
			}
			return l
		}
		return line{{"  ", ""}, {text, sText}}
	}
	out := []line{
		{{"Notify on these events, for PRs with alerts on (n).", sMuted}},
		{{"A job or thread with its own bell (b) notifies on every change.", sMuted}},
		{},
	}
	for i, e := range model.EventTypes {
		out = append(out, row(i, check(st.Events[e])+e.Label()))
	}
	out = append(out, line{}, row(len(model.EventTypes), check(st.Mute)+"global mute (keeps per-PR settings)"))
	return append(out, line{}, line{{"j/k move · space toggle · esc close", sSecondary}})
}

// helpSections groups the keys like jira-tabbed-tui's help panel.
var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Navigation", [][2]string{
		{"h / l, gT / gt", "previous / next tab"},
		{"1 – 9", "jump to tab N"},
		{"j / k, gg / G", "move between jobs or threads"},
		{"v", "switch between checks and comments"},
		{"e", "expand / fold a comment thread"},
		{"/", "find a job or thread"},
	}},
	{"Links", [][2]string{
		{"enter", "open the job or comment page"},
		{"o", "open the PR page"},
		{"y / Y", "copy PR / job or comment URL"},
	}},
	{"Notifications", [][2]string{
		{"n", "alerts for this PR"},
		{"b", "alerts for this job or thread"},
		{"m", "mute / restore this PR's alerts"},
		{"c", "clear all of this PR's alerts"},
		{"N", "alert settings, mute all"},
	}},
	{"Pull requests", [][2]string{
		{"a", "add a PR"},
		{"d", "unwatch this PR"},
		{"r", "poll now"},
		{"R", "re-run failed CI"},
	}},
	{"Other", [][2]string{
		{"? / esc", "close this panel"},
		{"q", "quit this TUI"},
	}},
}

// helpContent lays the help sections out in as few columns as fit the
// screen height.
func (m *Model) helpContent(w, h int) []line {
	const keyW, gap = 16, 3
	section := func(i int, descW int) []line {
		sec := helpSections[i]
		out := []line{{{sec.title, sBold + sMuted}}}
		for _, k := range sec.keys {
			out = append(out, line{{padRight(k[0], keyW), sBold + sAccent}, {truncate(k[1], descW), sText}})
		}
		return out
	}
	avail := h - 8 // borders, padding, title chip and its blank line
	for cols := 1; ; cols++ {
		descW := min(48, max((w-5-gap*(cols-1))/cols-keyW, 12))
		// Fill columns in order, starting a new one when the next section
		// would make the current one taller than an even share.
		var columns [][]line
		var cur []line
		total := 0
		for i := range helpSections {
			total += len(helpSections[i].keys) + 2
		}
		target := (total + cols - 1) / cols
		for i := range helpSections {
			s := section(i, descW)
			if len(cur) > 0 && len(cur)+1+len(s) > target && len(columns) < cols-1 {
				columns = append(columns, cur)
				cur = nil
			}
			if len(cur) > 0 {
				cur = append(cur, line{})
			}
			cur = append(cur, s...)
		}
		columns = append(columns, cur)
		height := 0
		for _, c := range columns {
			height = max(height, len(c))
		}
		if height <= avail || cols == 3 {
			colW := keyW + descW + gap
			out := make([]line, height)
			for r := 0; r < height; r++ {
				for c, col := range columns {
					var l line
					if r < len(col) {
						l = col[r]
					}
					if c < len(columns)-1 {
						l = fill(l, colW, "")
					}
					out[r] = append(out[r], l...)
				}
			}
			return out
		}
	}
}

func (m *Model) footer(w int) line {
	var right line
	if m.snap != nil && m.snap.Settings.Mute {
		right = append(right, seg{"muted · ", sSecondary})
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
		left = line{{"  Add PR (URL or owner/repo#N): ", sBold + sAccent}, {string(m.input) + "_", sStrong}}
	case m.mode == modeFind:
		left = line{{"  /", sBold + sAccent}, {string(m.input) + "_", sStrong}, {"   ↑/↓ choose · enter select · esc cancel", sSecondary}}
	case m.mode == modeConfirm:
		left = line{{"  " + m.confirmMsg + " ", sBold + sStrong}, {"[y/N]", sYellow}}
	case m.flash != "" && m.now().Before(m.flashUntil):
		// Like jira-tabbed-tui: bold green or red, with a mark.
		if m.flashErr {
			left = line{{"  " + model.Failed.Icon() + " " + m.flash, sBold + sRed}}
		} else {
			left = line{{"  " + model.Passed.Icon() + " " + m.flash, sBold + sGreen}}
		}
	case m.snap != nil && m.snap.Stale && m.snap.Error != "":
		left = line{{"  ! " + m.snap.Error, sYellow}}
	default:
		left = hintLine(w-right.width()-1, m.view)
	}
	return spread(left, right, w)
}

// footerHints are the key hints of the footer, in display order. Each says
// what the key does, in the checks list and, when it differs, in the
// comments list (a hint without a checks label is for comments only); drop is the order in which they give way on narrow
// screens (higher first). "? all keys" always stays.
var footerHints = []struct {
	key, label, comments string
	drop                 int
}{
	{"h/l", "prev/next PR", "", 3},
	{"j/k", "next/prev job", "next/prev thread", 4},
	{"v", "comments", "checks", 5},
	{"enter", "open job", "open comment", 1},
	{"e", "", "expand", 11}, // comments only
	{"o", "open PR", "", 2},
	{"y/Y", "copy PR/job link", "copy PR/comment link", 9},
	{"n", "alert PR", "", 6},
	{"b", "alert job", "alert thread", 7},
	{"N", "alert settings", "", 10},
	{"/", "find job", "find thread", 8},
	{"?", "all keys", "", 0},
}

// hintLine renders as many footer hints as fit in w cells, for the list
// shown: keys in the accent, what they do in grey.
func hintLine(w, view int) line {
	keep := make([]bool, len(footerHints))
	for i := range keep {
		keep[i] = true
	}
	build := func() line {
		l := line{{"  ", ""}}
		first := true
		for i, h := range footerHints {
			if !keep[i] || h.label == "" && view != viewComments {
				continue
			}
			if !first {
				l = append(l, seg{" · ", sDim})
			}
			first = false
			label := h.label
			if view == viewComments && h.comments != "" {
				label = h.comments
			}
			l = append(l, seg{h.key, sBold + sAccent}, seg{" " + label, sSecondary})
		}
		return l
	}
	for l := build(); ; l = build() {
		if l.width() <= w {
			return l
		}
		// Drop the hint that gives way first among those still shown.
		worst := -1
		for i, h := range footerHints {
			if keep[i] && h.drop > 0 && (worst < 0 || h.drop > footerHints[worst].drop) {
				worst = i
			}
		}
		if worst < 0 {
			return l
		}
		keep[worst] = false
	}
}
