package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/daemon"
	"github.com/clobrano/ghwatch/internal/model"
)

func TestStatus(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{ConfigDir: dir, StateDir: dir, RuntimeDir: dir}
	cfg := config.Default()
	item := func(s model.State, life model.Lifecycle) model.Item {
		c := []model.Check{{Name: "x", State: s}}
		return model.Item{Lifecycle: life, Checks: c}
	}
	snap := &model.Snapshot{Schema: 1, GeneratedAt: time.Now(), Items: []model.Item{
		item(model.Running, model.Open), item(model.Running, model.Open), item(model.Pending, model.Open),
		item(model.Passed, model.Open), item(model.Failed, model.Open), item(model.Failed, model.LifeMerged),
		{Lifecycle: model.Open, MergeQueue: &model.MergeQueue{State: "queued"}, Checks: []model.Check{{Name: "x", State: model.Passed}}},
	}}
	if err := daemon.WriteSnapshot(paths.Snapshot(), snap); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// No daemon holds the lock, so the summary is marked stale.
	if err := cmdStatus(paths, cfg, nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "PR ●3 ✓1 ⧗1 ✗1 ⚠\n" {
		t.Errorf("status = %q", got)
	}

	lock, err := daemon.AcquireLock(paths.Lock())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	out.Reset()
	if err := cmdStatus(paths, cfg, []string{"-format", "{{.Total}} {{.Merged}} {{.Stale}}"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "7 1 false\n" {
		t.Errorf("custom status = %q", got)
	}

	os.Remove(paths.Snapshot())
	out.Reset()
	cmdStatus(paths, cfg, nil, &out)
	if got := out.String(); got != "PR ⚠\n" {
		t.Errorf("status without snapshot = %q", got)
	}
}

func TestListAndChecksOutput(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{ConfigDir: dir, StateDir: dir, RuntimeDir: dir}
	os.WriteFile(filepath.Join(dir, "watch"), []byte("o/r#1\no/r#2\n"), 0o600)
	start := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Second)
	it := model.Item{ID: "pr:o/r#1", Repo: "o/r", Number: 1, Title: "Fix", HeadSHA: "abcdef1234", Lifecycle: model.Open,
		Checks: []model.Check{{Name: "unit", Source: "Actions", State: model.Passed, StartedAt: &start, CompletedAt: &end, Required: true, URL: "https://x/1"}}}
	daemon.WriteSnapshot(paths.Snapshot(), &model.Snapshot{Items: []model.Item{it}})

	var out bytes.Buffer
	if err := cmdList(paths, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "pr:o/r#1  ✓ passed  Fix") || !strings.Contains(got, "pr:o/r#2  ?") {
		t.Errorf("ls =\n%s", got)
	}

	out.Reset()
	printChecks(&out, it, end)
	for _, want := range []string{"✓ o/r#1 Fix", "head abcdef1", "1/1 done", "✓ unit", "1m30s", "Actions", "required", "https://x/1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("checks output lacks %q:\n%s", want, out.String())
		}
	}
}
