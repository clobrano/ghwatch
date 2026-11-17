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

func (m *Model) selectedThread() (*model.Item, *model.Thread) {
	it := m.current()
	if it == nil {
		return nil, nil
	}
	ts := m.threads(it)
	if len(ts) == 0 {
		return it, nil
	}
	return it, &ts[m.threadIndex(it.ID, ts)]
}

// threadIndex returns the index of the selected thread in ts.
func (m *Model) threadIndex(id string, ts []model.Thread) int {
	s := m.csel[id]
	if s.key != "" {
		for i, t := range ts {
			if t.ID == s.key {
				return i
			}
		}
	}
	return min(max(s.idx, 0), len(ts)-1)
}

func (m *Model) moveThread(delta int) {
	it := m.current()
	if it == nil {
		return
	}
	ts := m.threads(it)
	if len(ts) == 0 {
		return
	}
	i := min(max(m.threadIndex(it.ID, ts)+delta, 0), len(ts)-1)
	m.csel[it.ID] = selection{key: ts[i].ID, idx: i}
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
		return []line{{{" no comments fetched yet", sDim}}}
	}
	readAt := m.readMark(it)
	groups := make([]int, len(ts))
	for i, t := range ts {
		groups[i] = threadGroup(t, readAt)
	}
	sel := m.threadIndex(it.ID, ts)
	groupStyle := [...]string{sYellow, sAccent, sDim}
	return m.groupedRows("c\x00"+it.ID, groups, sel, rows,
		func(g, n int) line {
			return line{{" " + threadGroupNames[g], groupStyle[g] + sBold}, {" · " + strconv.Itoa(n), sDim}}
		},
		func(i int) line { return m.threadRow(it, ts[i], i == sel, w, readAt) })
}

// Comment list columns: marker, icon, thread, time, author.
const authorW = 16

func threadNameWidth(w int) int { return max(w-3-1-timeW-2-authorW, 8) }

// threadRow renders one row of the comment list, w cells wide: the
// thread with its comment count and latest comment, how long ago and by
// whom it was written.
func (m *Model) threadRow(it *model.Item, t model.Thread, selected bool, w int, readAt time.Time) line {
	nameW := threadNameWidth(w)
	marker, nameStyle := seg{" ", ""}, sText
	if selected {
		nameStyle = sBold + sWhite
		if !m.Color {
			marker = seg{"›", ""}
		}
	}
	ic := seg{commentMark(), sAccent}
	switch {
	case t.NewSince(readAt) > 0:
		ic = seg{unseenMark, sBold + sYellow}
	case t.Resolved:
		ic = seg{model.Passed.Icon(), sDim}
	}
	name := t.Name()
	if t.Outdated {
		name += " (outdated)"
	}
	cell := line{{" " + name, nameStyle}}
	if it.WatchingThread(t.ID) {
		cell = append(cell, seg{" " + bellIcon, bellStyle(it, sCyan)})
	}
	if n := t.NewSince(readAt); n > 0 {
		cell = append(cell, seg{" " + strconv.Itoa(n) + " new", sYellow})
	} else if t.Total > 0 {
		cell = append(cell, seg{" · " + strconv.Itoa(t.Total), sDim})
	}
	when, author := "", ""
	if c := t.Last(); c != nil {
		when = human(m.now().Sub(c.CreatedAt)) + " ago"
		author = c.Author
		if body := strings.TrimSpace(c.Body); body != "" {
			cell = append(cell, seg{"  " + body, sSecondary})
		}
	} else {
		cell = append(cell, seg{"  no comments yet", sDim})
	}
	l := append(line{marker, ic}, fill(cell, nameW+1, "")...)
	l = append(l, seg{" " + padLeft(when, timeW), sMuted}, seg{"  " + padRight(author, authorW), sSecondary})
	if selected && m.Color {
		return withStyle(fill(l, w, ""), bgSelect+sBold)
	}
	return l
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
		return []line{{{" no matching thread", sDim}}}
	}
	readAt := m.readMark(it)
	sel := min(m.findSel, len(matches)-1)
	m.findSel = sel
	out := []line{{{" " + strconv.Itoa(len(matches)) + " of " + strconv.Itoa(len(ts)) + " threads match", sDim}}}
	start := max(sel-(rows-1)+1, 0)
	for n, idx := range matches[start:] {
		if len(out) >= rows {
			break
		}
		out = append(out, m.threadRow(it, ts[idx], start+n == sel, w, readAt))
	}
	return out
}
