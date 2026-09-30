package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWait(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "opened")
	ok := filepath.Join(dir, "ok.sh")
	os.WriteFile(ok, []byte("#!/bin/sh\necho \"$@\" > "+log+"\n"), 0o700)
	if err := OpenWait(ok, "https://job/1"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); strings.TrimSpace(string(data)) != "https://job/1" {
		t.Errorf("browser got %q", data)
	}

	bad := filepath.Join(dir, "bad.sh")
	os.WriteFile(bad, []byte("#!/bin/sh\necho 'no display' >&2\nexit 4\n"), 0o700)
	err := OpenWait(bad, "https://job/1")
	if err == nil || !strings.Contains(err.Error(), "exit status 4") || !strings.Contains(err.Error(), "no display") {
		t.Errorf("failing browser: %v", err)
	}
}
