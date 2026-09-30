// Package ipc is the daemon's Unix-socket protocol: newline-delimited
// JSON messages. On connect a client receives the full snapshot, then a
// new snapshot after every change. Clients send commands and get one
// result per command; every state change a command causes is broadcast
// to all clients, the sender included.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/clobrano/ghwatch/internal/model"
)

// Command operations.
const (
	OpAdd     = "add"     // ID: URL or reference to watch
	OpUnwatch = "unwatch" // ID: item ID
	OpPoll    = "poll"    // poll now
	OpAlerts  = "alerts"  // ID, On: per-item notifications
	OpEvents  = "events"  // Events: enabled event types
	OpMute    = "mute"    // On: global mute
	OpRetest  = "retest"  // ID: re-trigger failed CI
)

// Message types.
const (
	TypeSnapshot = "snapshot"
	TypeCommand  = "command"
	TypeResult   = "result"
)

// Command is a request from a client.
type Command struct {
	Op     string                   `json:"op"`
	ID     string                   `json:"id,omitempty"`
	On     *bool                    `json:"on,omitempty"`
	Events map[model.EventType]bool `json:"events,omitempty"`
}

// Message is one line on the socket.
type Message struct {
	Type     string          `json:"type"`
	Seq      int64           `json:"seq,omitempty"`
	Snapshot *model.Snapshot `json:"snapshot,omitempty"`
	Command  *Command        `json:"command,omitempty"`
	Info     string          `json:"info,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// maxLine bounds one message; a snapshot of hundreds of items fits.
const maxLine = 16 << 20

// Handler is the daemon side of the protocol.
type Handler interface {
	// Snapshot returns the current state, sent to a client on connect.
	Snapshot() *model.Snapshot
	// Handle applies a command. It may block; only the sending client
	// waits for it.
	Handle(ctx context.Context, cmd Command) (info string, err error)
}

// Server accepts clients on a Unix socket.
type Server struct {
	ln      net.Listener
	handler Handler
	// QueueSize is the per-client outgoing queue length. A client whose
	// queue is full is disconnected; it reconnects and gets a fresh
	// snapshot, so a slow client never slows the others.
	QueueSize int

	mu      sync.Mutex
	clients map[*client]struct{}
	wg      sync.WaitGroup
}

type client struct {
	conn net.Conn
	out  chan []byte
	once sync.Once
}

func (c *client) close() { c.once.Do(func() { c.conn.Close() }) }

// Listen creates the socket at path with mode 0600. A leftover socket
// file is removed; the caller must hold the single-instance lock.
func Listen(path string, h Handler) (*Server, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	old := umask(0o177)
	ln, err := net.Listen("unix", path)
	umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &Server{ln: ln, handler: h, QueueSize: 64, clients: map[*client]struct{}{}}, nil
}

// Serve accepts clients until ctx is done.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.ln.Close()
		s.mu.Lock()
		for c := range s.clients {
			c.close()
		}
		s.mu.Unlock()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serveClient(ctx, conn)
		}()
	}
}

// Clients returns the number of connected clients.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Broadcast sends snap to every client.
func (s *Server) Broadcast(snap *model.Snapshot) {
	line, err := encode(Message{Type: TypeSnapshot, Snapshot: snap})
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		s.enqueue(c, line)
	}
}

// enqueue must be called with s.mu held.
func (s *Server) enqueue(c *client, line []byte) {
	select {
	case c.out <- line:
	default:
		delete(s.clients, c)
		c.close()
	}
}

func (s *Server) serveClient(ctx context.Context, conn net.Conn) {
	c := &client{conn: conn, out: make(chan []byte, s.QueueSize)}
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		c.close()
	}()

	// Register and queue the initial snapshot atomically with respect to
	// Broadcast, so the client never sees an older state after a newer one.
	first, err := encode(Message{Type: TypeSnapshot, Snapshot: s.handler.Snapshot()})
	if err != nil {
		return
	}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	c.out <- first
	s.mu.Unlock()

	go func() {
		for line := range c.out {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Write(line); err != nil {
				c.close()
				return
			}
		}
	}()
	defer close(c.out)

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		var m Message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil || m.Type != TypeCommand || m.Command == nil {
			continue
		}
		info, err := s.handler.Handle(ctx, *m.Command)
		res := Message{Type: TypeResult, Seq: m.Seq, Info: info}
		if err != nil {
			res.Error = err.Error()
		}
		line, err := encode(res)
		if err != nil {
			continue
		}
		s.mu.Lock()
		if _, ok := s.clients[c]; ok {
			s.enqueue(c, line)
		}
		s.mu.Unlock()
	}
	// Removing the client under the lock before close(c.out) guarantees
	// Broadcast never sends on a closed channel.
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

func encode(m Message) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Client is a connection to the daemon.
type Client struct {
	conn net.Conn
	seq  atomic.Int64

	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan Message
	snaps   chan *model.Snapshot
	done    chan struct{}
}

// ErrClosed is returned for commands on a closed connection.
var ErrClosed = errors.New("disconnected from daemon")

// Dial connects to the daemon socket.
func Dial(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		pending: map[int64]chan Message{},
		snaps:   make(chan *model.Snapshot, 1),
		done:    make(chan struct{}),
	}
	go c.read()
	return c, nil
}

// Snapshots delivers the latest snapshot; older undelivered snapshots are
// dropped. The channel is closed when the connection ends.
func (c *Client) Snapshots() <-chan *model.Snapshot { return c.snaps }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close ends the connection.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) read() {
	defer func() {
		c.conn.Close()
		c.mu.Lock()
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = nil
		c.mu.Unlock()
		close(c.snaps)
		close(c.done)
	}()
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case TypeSnapshot:
			if m.Snapshot == nil {
				continue
			}
			select { // keep only the newest
			case <-c.snaps:
			default:
			}
			c.snaps <- m.Snapshot
		case TypeResult:
			c.mu.Lock()
			ch := c.pending[m.Seq]
			delete(c.pending, m.Seq)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

// Do sends a command and waits for its result.
func (c *Client) Do(ctx context.Context, cmd Command) (string, error) {
	seq := c.seq.Add(1)
	ch := make(chan Message, 1)
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return "", ErrClosed
	}
	c.pending[seq] = ch
	c.mu.Unlock()

	line, err := encode(Message{Type: TypeCommand, Seq: seq, Command: &cmd})
	if err != nil {
		return "", err
	}
	c.wmu.Lock()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = c.conn.Write(line)
	c.wmu.Unlock()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrClosed, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return "", ErrClosed
		}
		if m.Error != "" {
			return m.Info, errors.New(m.Error)
		}
		return m.Info, nil
	case <-ctx.Done():
		c.mu.Lock()
		if c.pending != nil {
			delete(c.pending, seq)
		}
		c.mu.Unlock()
		return "", ctx.Err()
	}
}
