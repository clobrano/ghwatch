package tui

import (
	"os"
	"testing"
	"time"
)

func TestKeyReaderPause(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	kr := &keyReader{f: r, pausable: true, parked: make(chan struct{}), wake: make(chan struct{})}
	events := make(chan any, 8)
	go kr.run(events)
	next := func() any {
		select {
		case ev := <-events:
			return ev
		case <-time.After(2 * time.Second):
			return nil
		}
	}

	w.WriteString("j")
	if ev := next(); ev != keyMsg("j") {
		t.Fatalf("got %v, want j", ev)
	}
	// Paused, the input is the editor's: the reader takes none of it.
	kr.pause()
	w.WriteString("hello")
	r.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("editor read %q, %v", buf[:n], err)
	}
	r.SetReadDeadline(time.Time{})
	kr.resume()
	w.WriteString("k")
	if ev := next(); ev != keyMsg("k") {
		t.Fatalf("after resuming got %v, want k", ev)
	}
	select {
	case ev := <-events:
		t.Errorf("unexpected event %v", ev)
	default:
	}
}
