// Package browser opens links and copies text to the clipboard.
package browser

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Open opens url with command (from config), else $BROWSER, else xdg-open.
// $BROWSER may be a colon-separated list; the first that starts wins.
// The browser is started detached and not waited for.
func Open(command, url string) error {
	var candidates []string
	if command != "" {
		candidates = append(candidates, command)
	}
	if b := os.Getenv("BROWSER"); b != "" {
		candidates = append(candidates, strings.Split(b, ":")...)
	}
	candidates = append(candidates, "xdg-open", "open")
	var errs []error
	for _, c := range candidates {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		if _, err := exec.LookPath(fields[0]); err != nil {
			errs = append(errs, err)
			continue
		}
		args := fields[1:]
		if strings.Contains(c, "%s") {
			for i := range args {
				args[i] = strings.ReplaceAll(args[i], "%s", url)
			}
		} else {
			args = append(args, url)
		}
		cmd := exec.Command(fields[0], args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		if err := cmd.Start(); err != nil {
			errs = append(errs, err)
			continue
		}
		go cmd.Wait()
		return nil
	}
	return fmt.Errorf("cannot open %s: %w", url, errors.Join(errs...))
}

// Copy puts text on the clipboard with wl-copy, xclip or xsel. When none
// is available it writes an OSC 52 sequence to tty, which terminals and
// tmux (set-clipboard on) forward to the system clipboard.
func Copy(text string, tty io.Writer) error {
	tools := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	for _, t := range tools {
		if os.Getenv("WAYLAND_DISPLAY") == "" && t[0] == "wl-copy" {
			continue
		}
		if os.Getenv("DISPLAY") == "" && t[0] != "wl-copy" {
			continue
		}
		if _, err := exec.LookPath(t[0]); err != nil {
			continue
		}
		cmd := exec.Command(t[0], t[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return nil
		}
	}
	if tty == nil {
		return errors.New("no clipboard tool found")
	}
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
	if os.Getenv("TMUX") != "" {
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, err := io.WriteString(tty, seq)
	return err
}
