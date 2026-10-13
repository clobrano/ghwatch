package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if cfg, err := Load(path); err != nil || cfg.Interval != time.Minute || cfg.Notifier != "desktop" {
		t.Fatalf("defaults = %+v, %v", cfg, err)
	}
	os.WriteFile(path, []byte(`
# comment
interval = "90s"
browser = "firefox --new-tab" # trailing comment
status_template = 'PR #{{.Failed}}'

[notify]
backend = "exec"
command = "curl -d @- ntfy.sh/me"

[daemon]
autostart = false
`), 0o600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Interval != 90*time.Second || cfg.Browser != "firefox --new-tab" || cfg.StatusTemplate != "PR #{{.Failed}}" ||
		cfg.Notifier != "exec" || cfg.NotifyCommand != "curl -d @- ntfy.sh/me" || cfg.AutoStart {
		t.Errorf("cfg = %+v", cfg)
	}

	os.WriteFile(path, []byte("interval = 120\n"), 0o600)
	if cfg, err := Load(path); err != nil || cfg.Interval != 2*time.Minute {
		t.Errorf("integer interval = %+v, %v", cfg.Interval, err)
	}
	for _, bad := range []string{"interval = 1\n", "nope = 1\n", "interval\n", "autostart = \"yes\"\n"} {
		os.WriteFile(path, []byte(bad), 0o600)
		if _, err := Load(path); err == nil {
			t.Errorf("Load(%q) succeeded", bad)
		}
	}
}

func TestPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/c")
	t.Setenv("XDG_STATE_HOME", "/s")
	t.Setenv("XDG_RUNTIME_DIR", "/r")
	p := DefaultPaths()
	if p.Watchlist() != "/c/ghwatch/watch" || p.Snapshot() != "/s/ghwatch/snapshot.json" || p.Socket() != "/r/ghwatch/ghwatch.sock" {
		t.Errorf("paths = %+v", p)
	}
}

func TestIconsSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if cfg, _ := Load(path); cfg.Icons != "fancy" {
		t.Errorf("default icons = %q", cfg.Icons)
	}
	os.WriteFile(path, []byte("[ui]\nicons = \"safe\"\n"), 0o600)
	if cfg, err := Load(path); err != nil || cfg.Icons != "safe" {
		t.Errorf("safe: %q, %v", cfg.Icons, err)
	}
	os.WriteFile(path, []byte("icons = \"emoji\"\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("unknown icon set accepted")
	}
}

func TestNotifyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if cfg, _ := Load(path); cfg.NotifyTimeout != 30*time.Second {
		t.Errorf("default = %s", cfg.NotifyTimeout)
	}
	for in, want := range map[string]time.Duration{"[notify]\ntimeout = \"2m\"\n": 2 * time.Minute, "notify_timeout = 0\n": 0, "[notify]\ntimeout = 45\n": 45 * time.Second} {
		os.WriteFile(path, []byte(in), 0o600)
		if cfg, err := Load(path); err != nil || cfg.NotifyTimeout != want {
			t.Errorf("%q: %s, %v", in, cfg.NotifyTimeout, err)
		}
	}
	os.WriteFile(path, []byte("notify_timeout = \"-1s\"\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("negative timeout accepted")
	}
}
