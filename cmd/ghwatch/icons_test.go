package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/daemon"

	"github.com/clobrano/ghwatch/internal/model"
)

// TestMain pins the safe icon set, which the tests assert on; the default
// (fancy) set is tested on its own.
func TestMain(m *testing.M) {
	model.UseIcons("safe")
	os.Exit(m.Run())
}

func TestIconSetFromEnvironment(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	tests := []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"LANG": "en_US.UTF-8", "TERM": "xterm-256color"}, "fancy"},
		{map[string]string{"LANG": "C.utf8"}, "fancy"},
		{map[string]string{}, "fancy"},
		{map[string]string{"LANG": "C"}, "ascii"},
		{map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "POSIX"}, "ascii"},
		{map[string]string{"LANG": "C", "LC_CTYPE": "it_IT.UTF-8"}, "fancy"},
		{map[string]string{"LANG": "en_US.UTF-8", "TERM": "linux"}, "ascii"},
	}
	for _, tt := range tests {
		if got := iconSet("fancy", env(tt.vars)); got != tt.want {
			t.Errorf("%v: %s, want %s", tt.vars, got, tt.want)
		}
	}
	if got := iconSet("safe", env(map[string]string{"LANG": "en_US.UTF-8"})); got != "safe" {
		t.Errorf("safe setting: %s", got)
	}
}

func TestIconsCommand(t *testing.T) {
	var out bytes.Buffer
	cmdIcons("fancy", "ascii", &out)
	for _, want := range []string{">fancy   ✗         ✓", " safe    ×         √", " ascii   x         v", "In use now: ascii"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestStatusUsesIconSet(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{ConfigDir: dir, StateDir: dir, RuntimeDir: dir}
	snap := &model.Snapshot{Schema: 1, GeneratedAt: time.Now(), Items: []model.Item{
		{Lifecycle: model.Open, Checks: []model.Check{{Name: "x", State: model.Failed}}},
		{Lifecycle: model.Open, Checks: []model.Check{{Name: "x", State: model.Passed}}},
	}}
	daemon.WriteSnapshot(paths.Snapshot(), snap)
	lock, _ := daemon.AcquireLock(paths.Lock())
	defer lock.Release()
	defer model.UseIcons("safe")
	for set, want := range map[string]string{"fancy": "PR ✓1 ✗1\n", "safe": "PR √1 ×1\n", "ascii": "PR v1 x1\n"} {
		model.UseIcons(set)
		var out bytes.Buffer
		if err := cmdStatus(paths, config.Default(), nil, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != want {
			t.Errorf("%s: %q, want %q", set, out.String(), want)
		}
	}
}
