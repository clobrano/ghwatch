// Package watchlist reads and edits the watchlist file: plain text, one
// item per line, '#' starts a comment.
package watchlist

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Parser turns a line into a canonical item ID.
type Parser func(s string) (id string, err error)

// Load returns the canonical IDs listed in the file, in file order and
// without duplicates. Lines that do not parse are reported in bad but do
// not stop loading. A missing file is an empty watchlist.
func Load(path string, parse Parser) (ids []string, bad []error, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := entry(sc.Text())
		if line == "" {
			continue
		}
		id, err := parse(line)
		if err != nil {
			bad = append(bad, fmt.Errorf("%s:%d: %w", path, n, err))
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, bad, sc.Err()
}

func entry(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		// "owner/repo#123" contains '#': only treat it as a comment when
		// it starts the line or follows whitespace.
		for i >= 0 {
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				line = line[:i]
				break
			}
			j := strings.IndexByte(line[i+1:], '#')
			if j < 0 {
				break
			}
			i += j + 1
		}
	}
	return strings.TrimSpace(line)
}

// Add appends id to the file unless it is already listed. It reports
// whether the file changed.
func Add(path, id string, parse Parser) (bool, error) {
	ids, _, err := Load(path, parse)
	if err != nil {
		return false, err
	}
	for _, x := range ids {
		if x == id {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, id+"\n"...)
	return true, writeAtomic(path, data)
}

// Remove deletes every line naming id, keeping comments and other lines
// as they are. It reports whether the file changed.
func Remove(path, id string, parse Parser) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var out bytes.Buffer
	changed := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if e := entry(line); e != "" {
			if got, err := parse(e); err == nil && got == id {
				changed = true
				continue
			}
		}
		out.WriteString(line + "\n")
	}
	if err := sc.Err(); err != nil || !changed {
		return false, err
	}
	return true, writeAtomic(path, out.Bytes())
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".watch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
