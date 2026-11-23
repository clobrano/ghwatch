package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/model"
)

// Lists a PR can show: its checks or its comment threads.
const (
	viewChecks = iota
	viewComments
)

// commentMark flags new comments.
func commentMark() string { return model.CommentIcon() }

// Comment thread groups, in display order.
const (
	groupNew = iota
	groupOpen
	groupResolved
)

var threadGroupNames = [...]string{"New", "Open", "Resolved"}

// visit records what the current visit of it's tab shows: its belled jobs
// and threads, which lead their groups, and its comments' read mark,
// below which comments are not new. Both are taken when the tab is opened
// and kept while it stays open, so the list does not move under the user
// as they ring bells or read.
func (m *Model) visit(it *model.Item) {
	if m.pinnedID == it.ID {
		if m.shownReadAt.IsZero() {
			m.shownReadAt = it.CommentsReadAt // comments fetched since
		}
		return
	}
	m.pinnedID, m.pinned, m.pinnedThreads = it.ID, map[string]bool{}, map[string]bool{}
	for _, name := range it.WatchedChecks {
		m.pinned[name] = true
	}
	for _, id := range it.WatchedThreads {
		m.pinnedThreads[id] = true
	}
	m.shownReadAt = it.CommentsReadAt
}

// readMark is the time comments of it must be newer than to show as new
// in this visit.
func (m *Model) readMark(it *model.Item) time.Time {
	m.visit(it)
	return m.shownReadAt
}

// threadGroup puts a thread in its display group.
func threadGroup(t model.Thread, readAt time.Time) int {
	switch {
	case t.NewSince(readAt) > 0:
		return groupNew
	case t.Resolved:
		return groupResolved
	}
	return groupOpen
}

// threads returns it's comment threads in display order: new, open, then
// resolved. Within a group, belled threads come first, then the
// conversation, then the most recently commented.
func (m *Model) threads(it *model.Item) []model.Thread {
	readAt := m.readMark(it)
	out := append([]model.Thread(nil), it.Threads...)
	last := func(t model.Thread) time.Time {
		if c := t.Last(); c != nil {
			return c.CreatedAt
		}
		return time.Time{}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ga, gb := threadGroup(a, readAt), threadGroup(b, readAt); ga != gb {
			return ga < gb
		}
		if pa, pb := m.pinnedThreads[a.ID], m.pinnedThreads[b.ID]; pa != pb {
			return pa
		}
		if ca, cb := a.ID == model.ConversationID, b.ID == model.ConversationID; ca != cb {
			return ca
		}
		return last(a).After(last(b))
	})
	return out
}

// cpos is a selectable row of the comments list: a thread, or one of the
// comments of an expanded thread.
type cpos struct {
	thread  int // index in the displayed threads
	comment int // index in the thread's comments; -1 for the thread's row
}

func (m *Model) isExpanded(it *model.Item, t model.Thread) bool {
	return m.expanded[it.ID+"\x00"+t.ID]
}

// positions lists the selectable rows of the comments list, in order.
func (m *Model) positions(it *model.Item, ts []model.Thread) []cpos {
	var out []cpos
	for i, t := range ts {
		out = append(out, cpos{i, -1})
		if m.isExpanded(it, t) {
			for j := range t.Comments {
				out = append(out, cpos{i, j})
			}
		}
	}
	return out
}

// posKey identifies a row across refreshes: its thread's ID, plus the
// comment's ID for a comment.
func posKey(ts []model.Thread, p cpos) string {
	if p.comment < 0 {
		return ts[p.thread].ID
	}
	return ts[p.thread].ID + "\x00" + ts[p.thread].Comments[p.comment].ID
}

// selPos returns the selected row of the comments list. A selected
// comment that is gone, or whose thread was folded, falls back to its
// thread.
func (m *Model) selPos(it *model.Item, ts []model.Thread) cpos {
	ps := m.positions(it, ts)
	s := m.csel[it.ID]
	if s.key != "" {
		for _, p := range ps {
			if posKey(ts, p) == s.key {
				return p
			}
		}
		thread, _, _ := strings.Cut(s.key, "\x00")
		for _, p := range ps {
			if p.comment < 0 && ts[p.thread].ID == thread {
				return p
			}
		}
	}
	return ps[min(max(s.idx, 0), len(ps)-1)]
}

// selectedComment returns the selected thread, and the selected comment
// when a comment of an expanded thread is selected rather than the thread.
func (m *Model) selectedComment() (*model.Item, *model.Thread, *model.Comment) {
	it := m.current()
	if it == nil {
		return nil, nil, nil
	}
	ts := m.threads(it)
	if len(ts) == 0 {
		return it, nil, nil
	}
	p := m.selPos(it, ts)
	t := &ts[p.thread]
	if p.comment < 0 {
		return it, t, nil
	}
	return it, t, &t.Comments[p.comment]
}

func (m *Model) selectedThread() (*model.Item, *model.Thread) {
	it, t, _ := m.selectedComment()
	return it, t
}

// selectedLink is where enter and Y go: the selected comment, or the
// selected thread's latest.
func (m *Model) selectedLink() string {
	_, t, c := m.selectedComment()
	switch {
	case c != nil && c.URL != "":
		return c.URL
	case t != nil:
		return t.Link()
	}
	return ""
}

// selectThread selects the thread with index i in the displayed list.
func (m *Model) selectThread(it *model.Item, ts []model.Thread, i int) {
	ps := m.positions(it, ts)
	for n, p := range ps {
		if p.thread == i && p.comment < 0 {
			m.csel[it.ID] = selection{key: ts[i].ID, idx: n}
			return
		}
	}
}

// moveThread moves the selection by delta rows: from thread to thread,
// through the comments of expanded threads.
func (m *Model) moveThread(delta int) {
	it := m.current()
	if it == nil {
		return
	}
	ts := m.threads(it)
	if len(ts) == 0 {
		return
	}
	ps := m.positions(it, ts)
	cur := m.selPos(it, ts)
	i := 0
	for n, p := range ps {
		if p == cur {
			i = n
		}
	}
	i = min(max(i+delta, 0), len(ps)-1)
	m.csel[it.ID] = selection{key: posKey(ts, ps[i]), idx: i}
	m.cscroll = 0 // the next comment starts at its top
}

// markRead tells the daemon the user saw the active PR's comments: any
// key pressed while its tab is active counts, as for the unseen mark.
func (m *Model) markRead() {
	it := m.current()
	if it == nil || it.NewComments() == 0 || !m.connected || m.quit {
		return
	}
	last := it.LatestComment()
	if m.readSent[it.ID].Equal(last) {
		return
	}
	if m.readSent == nil {
		m.readSent = map[string]time.Time{}
	}
	m.readSent[it.ID] = last
	m.backend.Send(ipc.Command{Op: ipc.OpCommentsRead, ID: it.ID})
}

// commentsBody is the comment threads list of the current tab.
func (m *Model) commentsBody(w, rows int) []line {
	it := m.current()
	if it == nil {
		return nil
	}
	ts := m.threads(it)
	if len(ts) == 0 {
		if it.HeadSHA == "" {
			return nil
		}
		return []line{{{" no comments fetched yet", sSecondary}}}
	}
	readAt := m.readMark(it)
	groups := make([]int, len(ts))
	for i, t := range ts {
		groups[i] = threadGroup(t, readAt)
	}
	p := m.selPos(it, ts)
	names := threadNames(ts)
	// The selected thread stays in view with as much of its comments as
	// fits; a selected comment stays in view whole, as far as it fits.
	from, to := 0, -1
	expand := func(i int) ([]line, [][2]int) {
		if !m.isExpanded(it, ts[i]) {
			return nil, nil
		}
		sel := -1
		if i == p.thread {
			sel = p.comment
		}
		return m.commentLines(ts[i], w, readAt, sel)
	}
	// A selected comment taller than the list scrolls with J/K: the
	// lines above the scroll position give way to a marker saying so.
	scroll, height := 0, 0
	if p.comment >= 0 {
		_, spans := expand(p.thread)
		from, to = 1+spans[p.comment][0], 1+spans[p.comment][1]
		height = to - from + 1
		scroll = min(m.commentScroll(posKey(ts, p)), max(height-rows+1, 0))
		m.cscroll = scroll
		if scroll > 0 {
			from += scroll - 1
		}
	}
	groupStyle := [...]string{sNew, sAccent, sSecondary}
	out, first, last := m.groupedRows("c\x00"+it.ID, groups, p.thread, from, to, rows,
		func(g, n int) line {
			return line{{" " + threadGroupNames[g], groupStyle[g] + sBold}, {" · " + strconv.Itoa(n), sSecondary}}
		},
		func(i int) []line {
			lines, _ := expand(i)
			return append([]line{m.threadRow(it, ts[i], names[ts[i].ID], i == p.thread && p.comment < 0, w, readAt)}, lines...)
		})
	if p.comment >= 0 && len(out) > 0 {
		mark := func(text string) line {
			return line{{"   │", sBorder}, {"     " + text, sSecondary}}
		}
		if scroll > 0 && first >= 0 && first < len(out) {
			out[first] = mark("↑ " + plural(scroll, "line") + " above · K")
		}
		if last >= len(out) {
			out[len(out)-1] = mark("↓ " + plural(last-len(out)+2, "more line") + " · J")
		}
	}
	return out
}

// commentScroll is how many lines of the selected comment are scrolled
// past: 0 once another row is selected.
func (m *Model) commentScroll(key string) int {
	if m.cscrollKey != key {
		m.cscrollKey, m.cscroll = key, 0
	}
	return m.cscroll
}

// scrollComment scrolls the selected comment by delta lines; the list
// clamps it to the comment's length when drawn.
func (m *Model) scrollComment(delta int) {
	_, t, c := m.selectedComment()
	if c == nil {
		return
	}
	m.commentScroll(t.ID + "\x00" + c.ID)
	m.cscroll = max(m.cscroll+delta, 0)
}

// Comment list columns: marker, icon, thread, time, author.
const authorW = 16

// lastW fits the latest comment's age, e.g. "2d22h ago".
const lastW = 10

func threadNameWidth(w int) int { return max(w-3-1-authorW-1-lastW, 8) }

// threadRow renders one row of the comment list, w cells wide: the
// thread with its comment count and latest comment, how long ago and by
// whom it was written.
func (m *Model) threadRow(it *model.Item, t model.Thread, name string, selected bool, w int, readAt time.Time) line {
	nameW := threadNameWidth(w)
	marker, nameStyle := seg{" ", ""}, sText
	if selected {
		nameStyle = sBold + sStrong
		if !m.Color {
			marker = seg{"›", ""}
		}
	}
	ic := seg{commentMark(), sAccent}
	switch {
	case t.NewSince(readAt) > 0:
		ic = seg{unseenMark, sBold + sNew}
	case t.Resolved:
		ic = seg{model.Passed.Icon(), sSecondary}
	}
	cell := line{{" " + name, nameStyle}}
	if t.Outdated {
		cell = append(cell, seg{" outdated", sSecondary})
	}
	if it.WatchingThread(t.ID) {
		cell = append(cell, seg{" " + bellIcon, bellStyle(it, sCyan)})
	}
	// The count, then how many are new: always in this order and style.
	if t.Total > 0 {
		cell = append(cell, seg{" · " + strconv.Itoa(t.Total), sSecondary})
	}
	if n := t.NewSince(readAt); n > 0 {
		cell = append(cell, seg{" · " + strconv.Itoa(n) + " new", sNew})
	}
	when, author := "", ""
	if c := t.Last(); c != nil {
		when = human(m.now().Sub(c.CreatedAt)) + " ago"
		author = c.Author
		if body := c.Summary(); body != "" {
			cell = append(cell, seg{"  " + body, sSecondary})
		}
	} else {
		cell = append(cell, seg{"  no comments yet", sSecondary})
	}
	// The cell is cut a column short of the author, so a cut snippet's
	// "…" never touches the name.
	l := append(line{marker, ic}, fill(cell, nameW+1, "")...)
	l = append(l, seg{" " + padRight(author, authorW), sSecondary}, seg{" " + padLeft(when, lastW), sMuted})
	if selected && m.Color {
		return withStyle(fill(l, w, ""), bgSelect+sBold)
	}
	return l
}

// threadNames are the names the list shows for ts, by thread ID: a code
// thread is its file and line, with as many parent directories as it
// takes to tell files of the same name apart.
func threadNames(ts []model.Thread) map[string]string {
	names := map[string]string{}
	paths := map[string]bool{}
	for _, t := range ts {
		if t.Path != "" {
			paths[t.Path] = true
		}
	}
	suffix := func(path string, k int) string {
		parts := strings.Split(path, "/")
		return strings.Join(parts[max(len(parts)-k, 0):], "/")
	}
	for _, t := range ts {
		if t.Path == "" {
			names[t.ID] = t.Name()
			continue
		}
		k := 1
		for clash := true; clash && k < 32; k++ {
			clash = false
			for p := range paths {
				if p != t.Path && suffix(p, k) == suffix(t.Path, k) {
					clash = true
				}
			}
			if !clash {
				break
			}
		}
		name := suffix(t.Path, k)
		if t.Line > 0 {
			name += ":" + strconv.Itoa(t.Line)
		}
		names[t.ID] = name
	}
	return names
}

// toggleThread expands the selected thread to show each of its fetched
// comments in full, or folds it back to its latest; folding from one of
// its comments selects the thread.
func (m *Model) toggleThread() {
	it, t := m.selectedThread()
	if t == nil {
		return
	}
	if m.expanded == nil {
		m.expanded = map[string]bool{}
	}
	k := it.ID + "\x00" + t.ID
	if m.expanded[k] {
		delete(m.expanded, k)
		ts := m.threads(it)
		for i := range ts {
			if ts[i].ID == t.ID {
				m.selectThread(it, ts, i)
			}
		}
	} else {
		m.expanded[k] = true
	}
}

// commentLines are the lines of an expanded thread, oldest comment
// first: for each, a line with its author, age and review state, then its
// text, wrapped. New comments carry the new mark, and the selected one
// (sel, or -1) is highlighted. spans gives each comment's first and last
// line.
func (m *Model) commentLines(t model.Thread, w int, readAt time.Time, sel int) (out []line, spans [][2]int) {
	gutter := seg{"   │", sBorder}
	if t.Path != "" { // the list shows the file; here is the whole path
		out = append(out, line{gutter, {"   " + t.Name(), sSecondary}})
	}
	if n := t.Total - len(t.Comments); n > 0 {
		out = append(out, line{gutter, {"   ⋯ " + plural(n, "earlier comment") + " on GitHub", sSecondary}})
	}
	for i, c := range t.Comments {
		start := len(out)
		mark := seg{"   ", ""}
		if c.Counts() && c.CreatedAt.After(readAt) {
			mark = seg{" " + unseenMark + " ", sBold + sNew}
		}
		who := seg{c.Author, sText + sBold}
		if c.Mine {
			who.style = sMuted + sBold
		}
		head := line{gutter, mark, who, {" · " + human(m.now().Sub(c.CreatedAt)) + " ago", sSecondary}}
		if c.Review != "" && c.Review != "commented" {
			head = append(head, seg{" · " + strings.ReplaceAll(c.Review, "_", " "), sAccent})
		}
		if i == sel {
			if m.Color {
				head = withStyle(fill(head, w, ""), bgSelect+sBold)
			} else {
				head[0] = seg{"   ›", ""}
			}
		}
		out = append(out, head)
		for _, l := range wrap(c.Body, w-9) {
			out = append(out, line{gutter, {"     " + l, sText}})
		}
		spans = append(spans, [2]int{start, len(out) - 1})
	}
	return out, spans
}

// findThreads returns the current tab's comment threads (as displayed) and
// the indexes of those matching the find query, ignoring case, best
// first: the thread's name, or its latest comment's author or text.
func (m *Model) findThreads() ([]model.Thread, []int) {
	it := m.current()
	if it == nil {
		return nil, nil
	}
	ts := m.threads(it)
	q := strings.ToLower(string(m.input))
	type hit struct{ idx, score int }
	var hits []hit
	for i, t := range ts {
		text := t.Name()
		if c := t.Last(); c != nil {
			text += " " + c.Author + " " + c.Body
		}
		if s, ok := fuzzy(q, strings.ToLower(text)); ok {
			hits = append(hits, hit{i, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score < hits[b].score })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.idx
	}
	return ts, out
}

// findThreadsBody lists the threads of the current tab that match the
// find query.
func (m *Model) findThreadsBody(w, rows int) []line {
	it := m.current()
	ts, matches := m.findThreads()
	if len(matches) == 0 {
		return []line{{{" no matching thread", sSecondary}}}
	}
	readAt := m.readMark(it)
	names := threadNames(ts)
	sel := min(m.findSel, len(matches)-1)
	m.findSel = sel
	out := []line{{{" " + strconv.Itoa(len(matches)) + " of " + strconv.Itoa(len(ts)) + " threads match", sSecondary}}}
	start := max(sel-(rows-1)+1, 0)
	for n, idx := range matches[start:] {
		if len(out) >= rows {
			break
		}
		out = append(out, m.threadRow(it, ts[idx], names[ts[idx].ID], start+n == sel, w, readAt))
	}
	return out
}
