package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/clobrano/ghwatch/internal/model"
)

type handler struct {
	mu   sync.Mutex
	snap model.Snapshot
}

func (h *handler) Snapshot() *model.Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.snap
	return &s
}

func (h *handler) Handle(_ context.Context, cmd Command) (string, error) { return "ok " + cmd.Op, nil }

func serve(t *testing.T) (*Server, string) {
	dir, err := os.MkdirTemp("", "gwipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	srv, err := Listen(path, &handler{snap: model.Snapshot{Schema: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return srv, path
}

func TestSocketMode(t *testing.T) {
	_, path := serve(t)
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v", fi.Mode(), err)
	}
}

func TestSlowClientIsDropped(t *testing.T) {
	srv, path := serve(t)

	// A client that never reads.
	stuck, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer stuck.Close()
	for srv.Clients() < 1 {
		time.Sleep(5 * time.Millisecond)
	}

	// Enough data to fill the socket buffer and then the client's queue.
	big := &model.Snapshot{Schema: 1}
	for i := 0; i < 500; i++ {
		big.Items = append(big.Items, model.Item{ID: "pr:o/r#1", Title: "a fairly long title to fill buffers quickly"})
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2*srv.QueueSize+50; i++ {
			srv.Broadcast(big)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Broadcast blocked on a client that does not read")
	}
	deadline := time.Now().Add(5 * time.Second)
	for srv.Clients() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("stuck client was not dropped")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Other clients are unaffected.
	c, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Do(context.Background(), Command{Op: OpPoll}); err != nil {
		t.Fatal(err)
	}
}

func TestCommandResult(t *testing.T) {
	_, path := serve(t)
	c, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	<-c.Snapshots()
	info, err := c.Do(context.Background(), Command{Op: OpPoll})
	if err != nil || info != "ok poll" {
		t.Fatalf("Do = %q, %v", info, err)
	}
	c.Close()
	<-c.Done()
	if _, err := c.Do(context.Background(), Command{Op: OpPoll}); err == nil {
		t.Error("Do on a closed client succeeded")
	}
}
