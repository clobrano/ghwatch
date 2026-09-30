// Package config resolves ghwatch's XDG paths and reads config.toml.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Paths are the files and directories ghwatch uses.
type Paths struct {
	ConfigDir  string // $XDG_CONFIG_HOME/ghwatch
	StateDir   string // $XDG_STATE_HOME/ghwatch
	RuntimeDir string // $XDG_RUNTIME_DIR/ghwatch
}

// Watchlist is the path of the watchlist file.
func (p Paths) Watchlist() string { return filepath.Join(p.ConfigDir, "watch") }

// ConfigFile is the path of config.toml.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// Snapshot is the path of the persisted state snapshot.
func (p Paths) Snapshot() string { return filepath.Join(p.StateDir, "snapshot.json") }

// Log is where an auto-started daemon writes its output.
func (p Paths) Log() string { return filepath.Join(p.StateDir, "daemon.log") }

// Socket is the daemon's Unix socket.
func (p Paths) Socket() string { return filepath.Join(p.RuntimeDir, "ghwatch.sock") }

// Lock is the single-instance lock file.
func (p Paths) Lock() string { return filepath.Join(p.RuntimeDir, "ghwatch.lock") }

// DefaultPaths resolves paths from the XDG environment variables.
func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	xdg := func(env, fallback string) string {
		if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
			return v
		}
		return fallback
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" || !filepath.IsAbs(runtime) {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("ghwatch-%d", os.Getuid()))
	}
	return Paths{
		ConfigDir:  filepath.Join(xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "ghwatch"),
		StateDir:   filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "ghwatch"),
		RuntimeDir: filepath.Join(runtime, "ghwatch"),
	}
}

// Ensure creates the directories, private to the user.
func (p Paths) Ensure() error {
	for _, d := range []string{p.ConfigDir, p.StateDir, p.RuntimeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return os.Chmod(p.RuntimeDir, 0o700)
}

// DefaultStatusTemplate renders e.g. "PR ●3 ✓5 ✗1".
const DefaultStatusTemplate = `PR {{if .Running}}●{{.Running}} {{end}}{{if .Passed}}✓{{.Passed}} {{end}}{{if .Queued}}⧗{{.Queued}} {{end}}{{if .Failed}}✗{{.Failed}} {{end}}{{if .Stale}}⚠{{end}}`

// Config is the content of config.toml.
type Config struct {
	// Interval between polls.
	Interval time.Duration
	// Browser command used to open links; empty means $BROWSER, then xdg-open.
	Browser string
	// StatusTemplate is a text/template for `ghwatch status`.
	StatusTemplate string
	// Notifier selects the notification backend: "desktop", "exec" or "none".
	Notifier string
	// NotifyCommand is run for the "exec" notifier, with the event as JSON
	// on stdin (a plugin hook, e.g. for ntfy.sh or a chat webhook).
	NotifyCommand string
	// AutoStart lets clients spawn the daemon when it is not running.
	AutoStart bool
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Interval:       60 * time.Second,
		StatusTemplate: DefaultStatusTemplate,
		Notifier:       "desktop",
		AutoStart:      true,
	}
}

// Load reads config.toml; a missing file yields the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	kv, err := parseTOML(f)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range kv {
		var err error
		switch k {
		case "interval", "poll.interval":
			switch x := v.(type) {
			case int64:
				cfg.Interval = time.Duration(x) * time.Second
			case string:
				cfg.Interval, err = time.ParseDuration(x)
			default:
				err = fmt.Errorf("want seconds or a duration string")
			}
			if err == nil && cfg.Interval < 10*time.Second {
				err = fmt.Errorf("must be at least 10s")
			}
		case "browser":
			cfg.Browser, err = asString(v)
		case "status_template", "status.template":
			cfg.StatusTemplate, err = asString(v)
		case "notifier", "notify.backend":
			cfg.Notifier, err = asString(v)
		case "notify_command", "notify.command":
			cfg.NotifyCommand, err = asString(v)
		case "autostart", "daemon.autostart":
			b, ok := v.(bool)
			if !ok {
				err = fmt.Errorf("want true or false")
			}
			cfg.AutoStart = b
		default:
			err = fmt.Errorf("unknown key")
		}
		if err != nil {
			return cfg, fmt.Errorf("%s: %s: %w", path, k, err)
		}
	}
	return cfg, nil
}

func asString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("want a string")
	}
	return s, nil
}

// parseTOML reads the subset of TOML ghwatch needs: comments, [tables]
// (flattened as "table.key"), and string, integer and boolean values.
func parseTOML(f *os.File) (map[string]any, error) {
	out := map[string]any{}
	table := ""
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n)
		}
		key := strings.TrimSpace(k)
		if table != "" {
			key = table + "." + key
		}
		val, err := parseValue(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out[key] = val
	}
	return out, sc.Err()
}

func stripComment(s string) string {
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inStr != 0 && c == '\\' && inStr == '"':
			i++
		case inStr != 0 && c == inStr:
			inStr = 0
		case inStr == 0 && (c == '"' || c == '\''):
			inStr = c
		case inStr == 0 && c == '#':
			return s[:i]
		}
	}
	return s
}

func parseValue(s string) (any, error) {
	switch {
	case s == "true":
		return true, nil
	case s == "false":
		return false, nil
	case strings.HasPrefix(s, `"`):
		return strconv.Unquote(s)
	case strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`) && len(s) >= 2:
		return s[1 : len(s)-1], nil
	}
	i, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unsupported value %q", s)
	}
	return i, nil
}
