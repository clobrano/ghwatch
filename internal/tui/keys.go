package tui

import "unicode/utf8"

// Key names for non-printable keys; printable keys are the character.
const (
	kUp        = "up"
	kDown      = "down"
	kLeft      = "left"
	kRight     = "right"
	kEnter     = "enter"
	kEsc       = "esc"
	kBackspace = "backspace"
	kTab       = "tab"
	kShiftTab  = "shift+tab"
	kCtrlC     = "ctrl+c"
	kCtrlN     = "ctrl+n"
	kCtrlP     = "ctrl+p"
	kCtrlU     = "ctrl+u"
	kCtrlW     = "ctrl+w"
	kHome      = "home"
	kEnd       = "end"
	kPgUp      = "pgup"
	kPgDown    = "pgdown"
)

var escSeqs = map[string]string{
	"[A": kUp, "[B": kDown, "[C": kRight, "[D": kLeft,
	"OA": kUp, "OB": kDown, "OC": kRight, "OD": kLeft,
	"[H": kHome, "[F": kEnd, "OH": kHome, "OF": kEnd,
	"[1~": kHome, "[4~": kEnd, "[7~": kHome, "[8~": kEnd,
	"[5~": kPgUp, "[6~": kPgDown, "[Z": kShiftTab,
}

// parseKeys splits raw terminal input into keys.
func parseKeys(b []byte) []string {
	var keys []string
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			if len(b) == 1 {
				return append(keys, kEsc)
			}
			// CSI/SS3: ESC [ or ESC O, parameters, one final byte.
			if b[1] == '[' || b[1] == 'O' {
				end := 2
				for end < len(b) && (b[end] < 0x40 || b[end] > 0x7e) {
					end++
				}
				if end < len(b) {
					seq := string(b[1 : end+1])
					if k, ok := escSeqs[seq]; ok {
						keys = append(keys, k)
					}
					b = b[end+1:]
					continue
				}
			}
			keys = append(keys, kEsc)
			b = b[1:]
		case c == '\r' || c == '\n':
			keys, b = append(keys, kEnter), b[1:]
		case c == 0x7f || c == 0x08:
			keys, b = append(keys, kBackspace), b[1:]
		case c == '\t':
			keys, b = append(keys, kTab), b[1:]
		case c == 0x03:
			keys, b = append(keys, kCtrlC), b[1:]
		case c == 0x0e:
			keys, b = append(keys, kCtrlN), b[1:]
		case c == 0x10:
			keys, b = append(keys, kCtrlP), b[1:]
		case c == 0x15:
			keys, b = append(keys, kCtrlU), b[1:]
		case c == 0x17:
			keys, b = append(keys, kCtrlW), b[1:]
		case c < 0x20:
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError {
				keys = append(keys, string(r))
			}
			b = b[n:]
		}
	}
	return keys
}
