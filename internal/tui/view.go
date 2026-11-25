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
		return m.helpPanel(w, h)
	case modeEvents:
		return overlay(w, h, "Notifications", m.eventsContent())
	}
	out := []line{m.titleBar(w), m.tabBar(w), {{strings.Repeat("─", w), sBorder}}}
	// On a short screen the header's last rows (labels, alerts, comments)
	// give way, so the list and the footer keep their place.
	hdr := m.header(w)
	out = append(out, hdr[:min(len(hdr), max(h-len(out)-1-5, 1))]...)
	// The list's tabs, then its box: border, column header, rows, border.
	// The box ends after its last row; the footer stays at the bottom.
	out = append(out, m.listTabs())
	rows := max(h-len(out)-1-3, 1)
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
	content := []line{m.columnHeader(w - 2)}
	content = append(content, b[:min(len(b), rows)]...)
	if len(b) == 0 {
		content = append(content, line{})
	}
	out = append(out, box(content, w, sBorder, "", false)...)
	for len(out) < h-1 {
		out = append(out, line{})
	}
	return append(out, m.footer(w))
}

// listTabs is the row above the list naming the two lists a PR has,
// comments first, as tabs: the one shown is a highlighted chip. Each
// carries its own count: new comments, and failing (else running) jobs.
func (m *Model) listTabs() line {
	tab := func(name string, count seg, on bool) line {
		switch {
		case on && m.Color:
			l := line{{" " + name, bgAccent + sBold + sOnAccent}}
			if count.text != "" {
				l = append(l, seg{" " + count.text, bgAccent + sBold + sOnAccent})
			}
			return append(l, seg{" ", bgAccent})
		case on:
			if count.text != "" {
				name += " " + count.text
			}
			return line{{"[" + name + "]", ""}}
		}
		l := line{{" " + name, sMuted + sBold}}
		if count.text != "" {
			l = append(l, seg{" ", ""}, count)
		}
		return append(l, seg{" ", ""})
	}
	var comments, checks seg
	if it := m.current(); it != nil {
		if k := m.newShown(it); k > 0 {
			comments = seg{strconv.Itoa(k) + " new", sNew + sBold}
		}
		_, failed := model.Progress(it.Checks)
		running := 0
		for _, c := range it.Checks {
			if c.State == model.Running || c.State == model.Pending {
				running++
			}
		}
		switch {
		case failed > 0:
			checks = seg{strconv.Itoa(failed) + " failing", sRed + sBold}
		case running > 0:
			checks = seg{strconv.Itoa(running) + " running", sYellow + sBold}
		}
	}
	l := line{{" ", ""}}
	l = append(l, tab("Comments", comments, m.view == viewComments)...)
	l = append(l, seg{"  ", ""})
	l = append(l, tab("Checks", checks, m.view == viewChecks)...)
	return append(l, seg{"   v switches", sDim})
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
	unseen, comments := 0, 0
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
		if it.Unseen {
			unseen++
		}
	}
	// In words: how many PRs are in each state, then what is new.
	sep := seg{": ", sDim}
	for _, sc := range []struct {
		st   model.State
		word string
	}{{model.Failed, "failing"}, {model.Running, "running"}, {model.Passed, "passed"}, {model.Queued, "queued"}, {model.Merged, "merged"}} {
		if counts[sc.st] > 0 {
			left = append(left, sep, seg{fmt.Sprintf("%d %s", counts[sc.st], sc.word), stateStyle(sc.st)})
			sep = seg{" · ", sDim}
		}
	}
	if unseen > 0 {
		left = append(left, seg{"  │  ", sDim}, seg{fmt.Sprintf("%d changed", unseen), sBold + sNew})
	}
	if comments > 0 {
		pre := seg{"  │  ", sDim}
		if unseen > 0 {
			pre = seg{" · ", sDim}
		}
		left = append(left, pre, seg{plural(comments, "new comment"), sBold + sNew})
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
	// On a narrow screen the tagline gives way before the summary.
	if left.width()+right.width()+1 > w && len(left) > 1 && left[1].text == "  "+appTagline {
		left = append(line{left[0]}, left[2:]...)
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
	out := []line{m.titleLine(it, w)}
	row := func(label string, segs ...seg) {
		l := line{{" " + padRight(label+":", headerLabelW), sSecondary}}
		for i, s := range segs {
			if i > 0 {
				l = append(l, seg{" · ", sDim})
			}
			l = append(l, s)
		}
		out = append(out, l)
	}
	if it.Error != "" {
		row("error", seg{"! " + it.Error, sRed})
	}
	if it.HeadSHA == "" {
		if it.Error == "" {
			row("CI", seg{"waiting for the first poll…", sSecondary})
		}
		return out
	}
	// CI runs on the head commit: it closes the CI row.
	head := "on " + short(it.HeadSHA)
	if !it.PushedAt.IsZero() {
		head += ", pushed " + human(m.now().Sub(it.PushedAt)) + " ago"
	}
	row("CI", append(m.ciSegs(it), seg{head, sSecondary})...)
	if c := m.commentSegs(it); len(c) > 0 {
		row("comments", c...)
	}
	if a := alertSegs(it); len(a) > 0 {
		row("alerts", a...)
	}
	if r := m.reviewSegs(it); len(r) > 0 {
		row("review", r...)
	}
	if len(it.Added) > 0 {
		// Why ghwatch added the PR on its own.
		var why []string
		for _, r := range it.Added {
			switch r {
			case model.AddedReview:
				why = append(why, "your review was requested")
			case model.AddedAssigned:
				why = append(why, "assigned to you")
			default:
				why = append(why, r)
			}
		}
		row("added", seg{"automatically: " + strings.Join(why, ", "), sSecondary})
	}
	if len(it.Labels) > 0 {
		out = append(out, labelLine(it.Labels))
	}
	return out
}

// headerLabelW is the width of the header rows' labels ("comments:").
const headerLabelW = 10

// titleLine is the header's first line, like the top of GitHub's PR page:
// the state chip, the title, then the reference, with the author at the
// right. The title gives way first when space is short.
func (m *Model) titleLine(it *model.Item, w int) line {
	left := line{{" ", ""}}
	if it.HeadSHA != "" { // known once fetched
		left = append(left, m.lifecycleChip(it.Lifecycle), seg{" ", ""})
	}
	ref := seg{"  " + ref(*it), sAccent}
	var right line
	if it.Author != "" {
		right = line{{"@" + it.Author + " ", sSecondary}}
	}
	room := w - left.width() - strWidth(ref.text) - right.width() - 1
	if room < 10 { // too narrow: the reference gives way too
		right = nil
		room = w - left.width() - strWidth(ref.text) - 1
	}
	left = append(left, seg{truncate(it.Title, max(room, 1)), sBold + sStrong}, ref)
	return spread(left, right, w)
}

// ciSegs sums up the head's checks: the merge queue first while queued,
// then progress, failures (saying how many block the merge) and jobs
// still running.
func (m *Model) ciSegs(it *model.Item) []seg {
	var out []seg
	if q := it.MergeQueue; q != nil {
		out = append(out, m.queueSeg(q))
	}
	if len(it.Checks) == 0 {
		return append(out, seg{"no checks reported yet", sSecondary})
	}
	done, failed := model.Progress(it.Checks)
	out = append(out, seg{fmt.Sprintf("%d/%d done", done, len(it.Checks)), sMuted})
	anyRequired, requiredFailed, running := false, 0, 0
	for _, c := range it.Checks {
		anyRequired = anyRequired || c.Required
		if c.State == model.Failed && c.Required {
			requiredFailed++
		}
		if c.State == model.Running || c.State == model.Pending {
			running++
		}
	}
	if failed > 0 {
		text := fmt.Sprintf("%d failing", failed)
		switch {
		case !anyRequired:
		case requiredFailed == 0:
			text += ", none required"
		case requiredFailed < failed:
			text += fmt.Sprintf(" (%d required)", requiredFailed)
		default:
			text += ", all required"
		}
		out = append(out, seg{text, sRed})
	}
	if running > 0 {
		out = append(out, seg{fmt.Sprintf("%d running", running), sYellow})
	}
	return out
}

// commentSegs sums up the comments: how many are new in this visit and
// how many review threads are unresolved.
func (m *Model) commentSegs(it *model.Item) []seg {
	if len(it.Threads) == 0 {
		return nil
	}
	var out []seg
	if n := m.newShown(it); n > 0 {
		out = append(out, seg{plural(n, "new comment"), sNew + sBold})
	}
	open := 0
	for _, t := range it.Threads {
		if t.ID != model.ConversationID && !t.Resolved {
			open++
		}
	}
	if open > 0 {
		out = append(out, seg{plural(open, "unresolved thread"), sMuted})
	}
	if len(out) == 0 {
		out = append(out, seg{"nothing new", sSecondary})
	}
	return out
}

// reviewSegs says where the user stands as a reviewer: whether their
// review is requested (from them or a team), then their latest review.
func (m *Model) reviewSegs(it *model.Item) []seg {
	r := it.Review
	var out []seg
	if r.Requested {
		text := "requested from you"
		if r.Team != "" {
			text = "requested from " + r.Team
		}
		if r.Reviewed() {
			text += " again"
		}
		out = append(out, seg{text, sNew + sBold})
	}
	ago := ""
	if !r.At.IsZero() {
		ago = " · " + human(m.now().Sub(r.At)) + " ago"
	}
	switch r.State {
	case "approved":
		out = append(out, seg{"you approved" + ago, sGreen})
	case "changes_requested":
		out = append(out, seg{"you requested changes" + ago, sRed})
	case "commented":
		out = append(out, seg{"you commented" + ago, sMuted})
	case "dismissed":
		out = append(out, seg{"your review was dismissed" + ago, sMuted})
	case "pending":
		out = append(out, seg{"your review is a draft, not submitted", sYellow})
	case "":
		if r.Requested {
			out = append(out, seg{"not reviewed yet", sMuted})
		}
	default:
		out = append(out, seg{"you reviewed (" + r.State + ")" + ago, sMuted})
	}
	return out
}

// alertSegs says which notifications are set on it.
func alertSegs(it *model.Item) []seg {
	if it.Muted {
		// The muted alerts are restored as they were: no need to detail them.
		return []seg{{bellIcon + " muted (m restores)", sMuted}}
	}
	var out []seg
	if it.Alerts {
		out = append(out, seg{"on for the PR", sAccent})
	}
	if n := len(it.WatchedChecks); n > 0 {
		out = append(out, seg{bellIcon + " " + plural(n, "job"), sAccent})
	}
	if n := len(it.WatchedThreads); n > 0 {
		out = append(out, seg{bellIcon + " " + plural(n, "thread"), sAccent})
	}
	return out
}

// lifecycleChip is the PR's state, as on GitHub's PR page: OPEN on green,
// MERGED on mauve, CLOSED on red; bracketed without colors.
func (m *Model) lifecycleChip(l model.Lifecycle) seg {
	name, color := "OPEN", bgGreen
	switch l {
	case model.LifeMerged:
		name, color = "MERGED", bgMagenta
	case model.LifeClosed:
		name, color = "CLOSED", bgRed
	}
	if !m.Color {
		return seg{"[" + name + "]", ""}
	}
	return seg{" " + name + " ", color + sBold + sOnAccent}
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
	l := line{{" " + padRight("labels:", headerLabelW), sSecondary}}
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
	out, _, _ := m.groupedRows(it.ID, groups, sel, 0, -1, rows,
		func(g, n int) line {
			return line{{" " + groupNames[g], groupStyle[g] + sBold}, {" · " + strconv.Itoa(n), sSecondary}}
		},
		func(i int) []line { return []line{m.checkRow(it, checks[i], i == sel, w, anyRequired)} })
	return out
}

// groupedRows lays out a list whose entries are sorted by group: a header
// row per non-empty group, then its entries, scrolled so the selected
// entry is visible (with its group header when it is the first of its
// group). An entry is its row and, when expanded, lines below it. Lines
// from to to (-1: the last) of the selected entry are kept in view, as
// far as they fit, from the first. topKey keeps the scroll position of
// this list between frames. first and last are the rows those lines
// land on, which may be outside the rows returned.
func (m *Model) groupedRows(topKey string, groups []int, sel, from, to, rows int, header func(group, count int) line, entry func(i int) []line) (out []line, first, last int) {
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
	start := selRow
	if start > 0 && list[start-1].entry < 0 && list[start].line == 0 {
		start--
	}
	if start < top {
		top = start
	}
	if selEnd >= top+rows {
		top = min(selEnd-rows+1, selRow)
	}
	top = max(min(top, len(list)-rows), 0)
	m.top[topKey] = top

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
	return out, selRow - top, selEnd - top
}

// Job list columns: marker, icon, name (with its bell and opt tags),
// time, source.
const timeW, srcW = 14, 12

// nameWidth is the width of the name column in a w-wide list.
func nameWidth(w int) int { return max(w-3-1-timeW-2-srcW, 8) }

// columnHeader is the list's header row, like jira-tabbed-tui's.
func (m *Model) columnHeader(w int) line {
	if m.view == viewComments {
		return line{{"   " + padRight("Thread", threadNameWidth(w)) + " " + padRight("Author", authorW) + " " + padLeft("Last", lastW), sBold + sText}}
	}
	return line{{"   " + padRight("Check", nameWidth(w)) + " " + padLeft("Time", timeW) + "  " + padRight("Source", srcW), sBold + sText}}
}

// sources shortens the reporting apps' long names for the source column.
var sources = map[string]string{
	"GitHub Advanced Security": "GH Security",
	"GitHub Actions":           "Actions",
	"GitHub Code Scanning":     "GH Scanning",
}

func shortSource(s string) string {
	if short, ok := sources[s]; ok {
		return short
	}
	return s
}

// checkRow renders one row of the check list, w cells wide: the state
// icon carries the color, the rest is neutral. The selected row is a
// full-width bar, or carries a › marker without colors.
func (m *Model) checkRow(it *model.Item, c model.Check, selected bool, w int, anyRequired bool) line {
	nameW := nameWidth(w)
	marker, nameStyle := seg{" ", ""}, sText
	if selected {
		marker, nameStyle = seg{" ", ""}, sBold+sStrong
		if !m.Color {
			marker = seg{"›", ""}
		}
	}
	// The name ends with a bell when the job has its own alerts, and an
	// opt tag when it is not required to merge.
	name := line{{" " + c.Name, nameStyle}}
	if it.Watching(c.Name) {
		name = append(name, seg{" " + bellIcon, bellStyle(it, sCyan)})
	}
	if anyRequired && !c.Required && c.State != model.Queued {
		name = append(name, seg{" opt", sSecondary})
	}
	when, whenStyle := m.checkTime(c), sSecondary
	if c.State == model.Queued {
		when, whenStyle = queueStatus(it.MergeQueue)
	}
	l := append(line{marker, icon(c.State)}, fill(name, nameW+1, "")...)
	l = append(l, seg{padLeft(when, timeW), whenStyle},
		seg{"  " + padRight(shortSource(c.Source), srcW), sSecondary})
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
	return overlayTitled(w, h, line{{" " + title + " ", bgAccent + sBold + sOnAccent}}, content)
}

// overlayTitled is overlay with a title line of its own, e.g. page tabs.
func overlayTitled(w, h int, title line, content []line) []line {
	body := []line{title, {}}
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
// helpEntry is a line of the help panel: a key (or icon) and what it
// does (or means), with the key's style when it is not a key's.
type helpEntry struct{ key, desc, style string }

type helpSection struct {
	title string
	keys  []helpEntry
}

var helpSections = []helpSection{
	{"Navigation", []helpEntry{
		{"h / l, gT / gt", "previous / next tab", ""},
		{"1 – 9", "jump to tab N", ""},
		{"j / k, gg / G", "move a job, or a line in comments", ""},
		{"J / K", "next / prev comment, or job group", ""},
		{"v", "switch comments / checks", ""},
		{"/", "find a job or thread", ""},
	}},
	{"Comments", []helpEntry{
		{"e", "expand / fold a thread", ""},
		{"r", "reply, or comment on the PR", ""},
		{"R", "resolve / unresolve a thread", ""},
	}},
	{"Links", []helpEntry{
		{"enter", "open the job or comment page", ""},
		{"o", "open the PR page", ""},
		{"y / Y", "copy PR / job or comment URL", ""},
	}},
	{"Notifications", []helpEntry{
		{"n", "alerts for this PR", ""},
		{"b", "alerts for this job or thread", ""},
		{"m", "mute / restore this PR's alerts", ""},
		{"c", "clear all of this PR's alerts", ""},
		{"N", "alert settings, mute all", ""},
	}},
	{"Pull requests", []helpEntry{
		{"a", "add a PR", ""},
		{"d", "unwatch this PR", ""},
		{"p", "poll now", ""},
		{"R", "re-run failed CI (checks list)", ""},
	}},
	{"Other", []helpEntry{
		{"tab", "legend of icons and marks", ""},
		{"? / esc", "close this panel", ""},
		{"q", "quit this TUI", ""},
	}},
}

// helpPanel is the help panel: the keys, or (tab) the legend.
func (m *Model) helpPanel(w, h int) []line {
	chip := func(name string, on bool) seg {
		switch {
		case on && m.Color:
			return seg{" " + name + " ", bgAccent + sBold + sOnAccent}
		case on:
			return seg{"[" + name + "]", ""}
		}
		return seg{" " + name + " ", sMuted}
	}
	title := line{chip("Keybindings", m.helpPage == 0), {" ", ""}, chip("Legend", m.helpPage == 1), {"   tab switches", sDim}}
	return overlayTitled(w, h, title, m.helpContent(w, h))
}

// legend is the help panel's second page: what the icons and marks mean,
// drawn as they are on screen.
func (m *Model) legend() []helpSection {
	state := func(st model.State, desc string) helpEntry {
		return helpEntry{st.Icon(), desc, stateStyle(st) + sBold}
	}
	legend := []helpSection{
		{"States", []helpEntry{
			state(model.Failed, "failed"),
			state(model.Running, "running"),
			state(model.Pending, "pending"),
			state(model.Passed, "passed"),
			state(model.Skipped, "skipped"),
			state(model.Cancelled, "cancelled"),
			state(model.Queued, "in the merge queue"),
			state(model.Merged, "merged"),
			state(model.Closed, "closed"),
			{"tab icon", "state of the required checks", sBold + sMuted},
		}},
		{"Marks", []helpEntry{
			{unseenMark, "PR changed, or new comment", sBold + sNew},
			{commentMark() + " 3", "new comments on the PR", sBold + sNew},
			{bellIcon, "alerts on (grey while muted)", sBold + sCyan},
			{"opt", "job not required to merge", sSecondary},
			{model.Passed.Icon(), "resolved comment thread", sSecondary},
			{"OPEN", "PR state; also MERGED, CLOSED", sBold + sGreen},
		}},
	}
	return legend
}

// helpContent lays the help sections out in as few columns as fit the
// screen height.
func (m *Model) helpContent(w, h int) []line {
	const keyW, gap = 16, 3
	sections := helpSections
	if m.helpPage == 1 {
		sections = m.legend()
	}
	section := func(i int, descW int) []line {
		sec := sections[i]
		out := []line{{{sec.title, sBold + sMuted}}}
		for _, k := range sec.keys {
			style := k.style
			if style == "" {
				style = sBold + sAccent
			}
			out = append(out, line{{padRight(k.key, keyW), style}, {truncate(k.desc, descW), sText}})
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
		for i := range sections {
			total += len(sections[i].keys) + 2
		}
		target := (total + cols - 1) / cols
		for i := range sections {
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
		left = hintLine(w-right.width()-1, m.view, m.view == viewComments && m.inThread())
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
	// inThread: shown only while the cursor is inside an expanded thread.
	inThread bool
}{
	{"h/l", "PR", "", 2, false},
	{"j/k", "job", "line", 3, false},
	{"J/K", "", "next/prev comment", 4, true},
	{"enter", "open", "", 1, false},
	{"e", "", "expand", 7, false}, // comments only
	{"r", "", "reply", 8, false},
	{"b", "bell", "", 6, false},
	{"v", "comments", "checks", 5, false},
	{"?", "help", "", 0, false},
}

// hintLine renders as many footer hints as fit in w cells, for the list
// shown: keys in the accent, what they do in grey.
func hintLine(w, view int, inThread bool) line {
	keep := make([]bool, len(footerHints))
	for i := range keep {
		keep[i] = true
	}
	build := func() line {
		l := line{{"  ", ""}}
		first := true
		for i, h := range footerHints {
			if !keep[i] || h.label == "" && view != viewComments || h.inThread && !inThread {
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
