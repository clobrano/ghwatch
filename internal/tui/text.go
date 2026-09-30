package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ANSI styles.
const (
	sReset   = "\x1b[0m"
	sBold    = "\x1b[1m"
	sDim     = "\x1b[2m"
	sReverse = "\x1b[7m"
	sRed     = "\x1b[31m"
	sGreen   = "\x1b[32m"
	sYellow  = "\x1b[33m"
	sBlue    = "\x1b[34m"
	sMagenta = "\x1b[35m"
	sCyan    = "\x1b[36m"
)

// seg is a run of text in one style.
type seg struct {
	text  string
	style string
}

// line is a row of styled segments.
type line []seg

func (l line) width() int {
	w := 0
	for _, s := range l {
		w += strWidth(s.text)
	}
	return w
}

// plain returns the text without styles.
func (l line) plain() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.text)
	}
	return b.String()
}

// render writes the line, cut to width cells.
func (l line) render(width int, color bool) string {
	var b strings.Builder
	left := width
	for _, s := range l {
		if left <= 0 {
			break
		}
		t := truncate(s.text, left)
		left -= strWidth(t)
		if color && s.style != "" {
			b.WriteString(s.style + t + sReset)
		} else {
			b.WriteString(t)
		}
	}
	return b.String()
}

// spread places right at the right edge of a width-wide line after left.
func spread(left, right line, width int) line {
	gap := width - left.width() - right.width()
	if gap < 1 {
		// Keep the right part (status) and cut the left one.
		lw := width - right.width() - 1
		if lw < 0 {
			return right
		}
		cut := line{}
		for _, s := range left {
			if lw <= 0 {
				break
			}
			t := truncate(s.text, lw)
			lw -= strWidth(t)
			cut = append(cut, seg{t, s.style})
		}
		left, gap = cut, width-cut.width()-right.width()
	}
	out := append(line{}, left...)
	out = append(out, seg{strings.Repeat(" ", max(gap, 0)), ""})
	return append(out, right...)
}

func runeWidth(r rune) int {
	switch {
	case r == 0 || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200B:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf && r != 0x303f,
		r >= 0xac00 && r <= 0xd7a3, r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff, r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func strWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// truncate cuts s to at most w cells, ending with … when cut.
func truncate(s string, w int) string {
	if strWidth(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// padRight pads or cuts s to exactly w cells.
func padRight(s string, w int) string {
	s = truncate(s, w)
	return s + strings.Repeat(" ", max(w-strWidth(s), 0))
}

// padLeft right-aligns s in w cells.
func padLeft(s string, w int) string {
	s = truncate(s, w)
	return strings.Repeat(" ", max(w-strWidth(s), 0)) + s
}

// human formats a duration compactly: 41s, 23m, 1h05m, 2d3h.
func human(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
