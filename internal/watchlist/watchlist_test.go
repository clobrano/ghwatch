package watchlist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parse(s string) (string, error) {
	s = strings.TrimPrefix(s, "pr:")
	if !strings.Contains(s, "#") {
		return "", fmt.Errorf("bad item %q", s)
	}
	return "pr:" + strings.ToLower(s), nil
}

func TestLoadAddRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch")
	orig := "# my PRs\nOrg/Repo#1   # lease fix\n\nbogus\npr:org/repo#2\norg/repo#1\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	ids, bad, err := Load(path, parse)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "pr:org/repo#1,pr:org/repo#2" {
		t.Errorf("ids = %v", ids)
	}
	if len(bad) != 1 || !strings.Contains(bad[0].Error(), ":4:") {
		t.Errorf("bad = %v", bad)
	}

	if changed, err := Add(path, "pr:org/repo#2", parse); err != nil || changed {
		t.Errorf("Add existing = %v, %v", changed, err)
	}
	if changed, err := Add(path, "pr:org/repo#3", parse); err != nil || !changed {
		t.Errorf("Add new = %v, %v", changed, err)
	}
	if changed, err := Remove(path, "pr:org/repo#1", parse); err != nil || !changed {
		t.Errorf("Remove = %v, %v", changed, err)
	}
	data, _ := os.ReadFile(path)
	want := "# my PRs\n\nbogus\npr:org/repo#2\npr:org/repo#3\n"
	if string(data) != want {
		t.Errorf("file =\n%s\nwant\n%s", data, want)
	}
	if changed, _ := Remove(path, "pr:org/repo#9", parse); changed {
		t.Error("removing an unknown item changed the file")
	}
}

func TestMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "watch")
	ids, _, err := Load(path, parse)
	if err != nil || len(ids) != 0 {
		t.Fatalf("Load missing = %v, %v", ids, err)
	}
	if changed, err := Add(path, "pr:a/b#1", parse); err != nil || !changed {
		t.Fatalf("Add to missing file = %v, %v", changed, err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("stat = %v, %v", fi, err)
	}
}
