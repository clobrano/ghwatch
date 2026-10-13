package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clobrano/ghwatch/internal/model"
)

func TestFormat(t *testing.T) {
	it := model.Item{Repo: "org/repo", Number: 123, Title: "Fix lease race", URL: "https://pr", HeadSHA: "a1b2c3d4e5", Lifecycle: model.LifeMerged}
	tests := []struct {
		tr     model.Transition
		title  string
		url    string
		urgent bool
	}{
		{model.Transition{Type: model.EventCheckFailed, Check: "e2e-aws", URL: "https://job"}, "× e2e-aws failed", "https://job", true},
		{model.Transition{Type: model.EventCheckStarted, Check: "unit", URL: "https://job"}, "* unit started", "https://job", false},
		{model.Transition{Type: model.EventAllPassed}, "√ all checks passed", "https://pr", false},
		{model.Transition{Type: model.EventCheckFinished, Check: "e2e", To: model.Passed, URL: "https://job"}, "√ e2e passed", "https://job", false},
		{model.Transition{Type: model.EventRestarted}, "o CI restarted by a new push (a1b2c3d)", "https://pr", false},
		{model.Transition{Type: model.EventFinished, To: model.Merged}, "M merged", "https://pr", false},
	}
	for _, tt := range tests {
		n := Format(tt.tr, it)
		if n.Title != tt.title || n.URL != tt.url || n.Urgent != tt.urgent || n.Body != "org/repo#123 Fix lease race" {
			t.Errorf("Format(%s) = %+v", tt.tr.Type, n)
		}
	}
}

func TestExec(t *testing.T) {
	out := filepath.Join(t.TempDir(), "event.json")
	n := Notification{Title: "× unit failed", URL: "https://job", Event: model.Transition{Type: model.EventCheckFailed}}
	if err := (Exec{Command: "cat > " + out}).Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	var got Notification
	if err := json.Unmarshal(data, &got); err != nil || got.Title != n.Title || got.Event.Type != model.EventCheckFailed {
		t.Errorf("plugin got %s (%v)", data, err)
	}
	err := (Exec{Command: "echo boom >&2; exit 3"}).Notify(context.Background(), n)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command: %v", err)
	}
}

func TestDesktopExpiry(t *testing.T) {
	// A stand-in notify-send records its arguments, one call per line.
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(dir, "notify-send"), []byte("#!/bin/sh\n[ \"$1\" = --help ] && exit 0\necho \"$*\" >> "+log+"\n"), 0o700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := &Desktop{Expire: 30 * time.Second}
	ctx := context.Background()
	if err := d.Notify(ctx, Notification{Title: "started", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Notify(ctx, Notification{Title: "failed", Body: "b", Urgent: true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(calls) != 2 {
		t.Fatalf("calls = %q", calls)
	}
	if !strings.Contains(calls[0], "--expire-time=30000") || strings.Contains(calls[0], "critical") {
		t.Errorf("normal notification: %q", calls[0])
	}
	if !strings.Contains(calls[1], "--expire-time=30000") || strings.Contains(calls[1], "critical") {
		t.Errorf("urgent notification should not stick: %q", calls[1])
	}
}
