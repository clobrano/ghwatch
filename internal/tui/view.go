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
	if m.mode == modeFind {
		b = m.findBody(w-2, rows)
	} else {
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
	out = append(out, box(content, w, sBorder, "", false)...)
	return append(out, m.footer(w))
}

// titleBar is the top line: the app name, how many PRs are watched and
// in which state, how many have alerts, and when GitHub was last polled.
func (m *Model) titleBar(w int) line {
	left := line{{" " + appTitle, sBold + sAccent}, {"  " + appTagline, sDim}}
	if m.snap == nil {
		return spread(left, line{{"waiting for the daemon… ", sDim}}, w)
	}
	items := m.items()
	prs := "PRs"
	if len(items) == 1 {
		prs = "PR"
	}
	left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%d %s", len(items), prs), sMuted})
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
		l := line{{"  " + strconv.Itoa(i+1) + ":", sMuted}, icon(st), {" " + name, sMuted}}
		if it.Alerts || len(it.WatchedChecks) > 0 {
			l = append(l, seg{" " + bellIcon, sAccent})
		}
		l = append(l, seg{"  ", ""})
		if i == m.activeIdx {
			// Like jira-tabbed-tui: bold white on the accent.
			for j := range l {
				l[j].style = bgAccent + sBold + sWhite
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
		return line{{" no watched items", sDim}}
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
		out = append(out, seg{"‹", sDim})
		used++
	}
	for i := start; i < len(labels); i++ {
		lw := labels[i].width()
		reserve := 0
		if i < len(labels)-1 {
			reserve = 1
		}
		if used+lw+reserve > w && i > start {
			out = append(out, seg{"›", sDim})
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
		return []line{{{" Nothing watched yet", sBold}}, {{" press a to add a PR, or run: ghwatch add <PR URL>", sDim}}}
	}
	title := line{{" " + ref(*it), sBold + sAccent}, {"  " + it.Title, sBold + sWhite}}
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
		add(seg{"waiting for the first poll…", sDim})
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
	if it.Alerts {
		add(seg{"alerts on", sAccent})
	}
	if n := len(it.WatchedChecks); n > 0 {
		jobs := "jobs"
		if n == 1 {
			jobs = "job"
		}
		add(seg{fmt.Sprintf("%s %d %s", bellIcon, n, jobs), sAccent})
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

// labelLine shows labels as chips in their GitHub colors.
func labelLine(labels []model.Label) line {
	l := line{{" ", ""}}
	for i, lb := range labels {
		if i > 0 {
			l = append(l, seg{" ", ""})
		}
		l = append(l, seg{" " + lb.Name + " ", labelStyle(lb.Color)})
	}
	return l
}

// labelStyle is a 24-bit background in the label's color, with black or
// white text, whichever reads better on it.
func labelStyle(hex string) string {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil || len(hex) != 6 {
		return sReverse
	}
	fg := "38;2;255;255;255"
	if r*299+g*587+b*114 > 150*1000 {
		fg = "38;2;0;0;0"
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%d;%sm", r, g, b, fg)
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
		marker, nameStyle = seg{" ", ""}, sBold+sWhite
		if !m.Color {
			marker = seg{"›", ""}
		}
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
		seg{"  " + padRight(c.Source, srcW), sSecondary})
	if anyRequired && !c.Required && c.State != model.Queued {
		l = append(l, seg{" opt", sDim})
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

// overlay draws a panel centered on an empty screen, like
// jira-tabbed-tui's keybindings panel: an accent border, a dark background
// and a title chip.
func overlay(w, h int, title string, content []line) []line {
	body := []line{{{" " + title + " ", bgAccent + sBold + sWhite}}, {}}
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
	panel := box(padded, bw, sAccent, "", rounded())
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
			l := line{{"› ", sAccent + sBold}, {text, sBold + sWhite}}
			if m.Color {
				return withStyle(fill(l, rowW, ""), bgSoft)
			}
			return l
		}
		return line{{"  ", ""}, {text, sText}}
	}
	out := []line{
		{{"Notify on these events, for PRs with alerts on (n).", sMuted}},
		{{"A job with its own bell (b) notifies on every change.", sMuted}},
		{},
	}
	for i, e := range model.EventTypes {
		out = append(out, row(i, check(st.Events[e])+e.Label()))
	}
	out = append(out, line{}, row(len(model.EventTypes), check(st.Mute)+"global mute (keeps per-PR settings)"))
	return append(out, line{}, line{{"j/k move · space toggle · esc close", sDim}})
}

// helpSections groups the keys like jira-tabbed-tui's help panel.
var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Navigation", [][2]string{
		{"h / l, gT / gt", "previous / next tab"},
		{"1 – 9", "jump to tab N"},
		{"j / k, gg / G", "move between jobs"},
		{"/", "find a job by name"},
	}},
	{"Links", [][2]string{
		{"enter", "open the job page"},
		{"o", "open the PR page"},
		{"y / Y", "copy PR / job URL"},
	}},
	{"Notifications", [][2]string{
		{"n", "alerts for this PR"},
		{"b", "alerts for this job"},
		{"N", "alert settings, mute"},
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
		left = line{{"  Add PR (URL or owner/repo#N): ", sBold + sAccent}, {string(m.input) + "_", sWhite}}
	case m.mode == modeFind:
		left = line{{"  /", sBold + sAccent}, {string(m.input) + "_", sWhite}, {"   ↑/↓ choose · enter select · esc cancel", sDim}}
	case m.mode == modeConfirm:
		left = line{{"  " + m.confirmMsg + " ", sBold + sWhite}, {"[y/N]", sYellow}}
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
		left = line{{"  h/l tab · j/k check · enter job · o PR · n/b alerts · N events · ? help", sDim}}
	}
	return spread(left, right, w)
}
