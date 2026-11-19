package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/clobrano/ghwatch/internal/model"
)

// Styles. The palette follows jira-tabbed-tui: a #5555ff accent, light
// grey text, darker greys for secondary text, hints and borders, and a
// #3333aa bar for the selected row. Colors are 24-bit.
const (
	sReset   = "\x1b[0m"
	sBold    = "\x1b[1m"
	sReverse = "\x1b[7m"
)

// Styles by role. UseTheme sets their colors.
var (
	sText        string // normal text
	sStrong      string // emphasis: titles, selected rows (with bold)
	sMuted       string // inactive tabs, details
	sSecondary   string // secondary content: authors, sources, counts, quiet states
	sDim         string // decoration only: separators, hints
	sBorder      string // box and rule lines
	sAccent      string // keys, PR reference, group headers
	sCyan        string // bells
	sPanelBorder string // help and settings panels
	sRed         string // failed
	sGreen       string // passed
	sYellow      string // running, stale, warnings
	sBlue        string // merge queue
	sMagenta     string // merged
	sNew         string // new comments, unseen changes
	sOnAccent    string // text on bgAccent

	bgAccent  string // active tab, title chips
	bgSelect  string // selected row
	bgSoft    string // selected row in panels
	bgOverlay string // help and settings panels
	bgGreen   string // open PR chip
	bgMagenta string // merged PR chip
	bgRed     string // closed PR chip
)

// fg and bg return 24-bit color escapes for a hex color.
func fg(hex string) string { return rgb(38, hex) }
func bg(hex string) string { return rgb(48, hex) }

func rgb(layer int, hex string) string {
	var r, g, b int
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", layer, r, g, b)
}

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

// withStyle returns l with style added before each segment's own style,
// e.g. a background for a highlighted row.
func withStyle(l line, style string) line {
	out := make(line, len(l))
	for i, s := range l {
		out[i] = seg{s.text, style + s.style}
	}
	return out
}

// fill pads l with spaces in style to exactly w cells.
func fill(l line, w int, style string) line {
	if n := w - l.width(); n > 0 {
		return append(append(line{}, l...), seg{strings.Repeat(" ", n), style})
	}
	return cut(l, w)
}

// cut keeps the first w cells of l.
func cut(l line, w int) line {
	var out line
	left := w
	for _, s := range l {
		if left <= 0 {
			break
		}
		t := truncate(s.text, left)
		left -= strWidth(t)
		out = append(out, seg{t, s.style})
	}
	return out
}

// box draws content inside a border of the given style, w cells wide.
// Content lines are padded with spaces in fillStyle.
func box(content []line, w int, borderStyle, fillStyle string, rounded bool) []line {
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	if rounded {
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
	}
	inner := w - 2
	out := []line{{{tl + strings.Repeat("─", inner) + tr, borderStyle}}}
	for _, c := range content {
		l := line{{"│", borderStyle}}
		l = append(l, fill(c, inner, fillStyle)...)
		out = append(out, append(l, seg{"│", borderStyle}))
	}
	return append(out, line{{bl + strings.Repeat("─", inner) + br, borderStyle}})
}

// rounded reports whether boxes may use rounded corners: only with the
// fancy icon set, since several fonts lack them.
func rounded() bool { return model.IconSet() == "fancy" }

// wrap breaks text into lines of at most w cells, at spaces where it can.
// Each line of the text keeps its indentation; runs of blank lines become
// one.
func wrap(text string, w int) []string {
	w = max(w, 8)
	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimRight(para, " \t")
		if para == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		indent := para[:len(para)-len(strings.TrimLeft(para, " \t"))]
		indent = strings.ReplaceAll(indent, "\t", "    ")
		if strWidth(indent) > w/2 {
			indent = ""
		}
		line := indent
		for _, word := range strings.Fields(para) {
			for strWidth(word) > w-strWidth(indent) { // longer than a line: cut it
				if strings.TrimSpace(line) != "" {
					out = append(out, line)
				}
				head := takeCells(word, w-strWidth(indent))
				out = append(out, indent+head)
				word, line = word[len(head):], indent
			}
			switch {
			case strings.TrimSpace(line) == "":
				line = indent + word
			case strWidth(line)+1+strWidth(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = indent + word
			}
		}
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// takeCells returns the longest prefix of s at most w cells wide, and at
// least one rune.
func takeCells(s string, w int) string {
	used := 0
	for i, r := range s {
		if used+runeWidth(r) > w && i > 0 {
			return s[:i]
		}
		used += runeWidth(r)
	}
	return s
}
