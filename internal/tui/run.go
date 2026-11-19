package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/clobrano/ghwatch/internal/browser"
	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/daemon"
	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/model"
)

// Options configure Run.
type Options struct {
	Paths  config.Paths
	Config config.Config
}

type (
	keyMsg    string
	snapMsg   *model.Snapshot
	connMsg   bool
	resultMsg struct {
		cmd  ipc.Command
		info string
		err  error
	}
	resizeMsg struct{}
	tickMsg   struct{}
	quitMsg   struct{}
)

// backend is the live Backend: commands go to the daemon connection,
// links open in the browser.
type backend struct {
	opts   Options
	events chan<- any
	tty    *os.File
	// suspend hands the terminal over (to an editor) and returns the
	// function that takes it back; nil when that is not possible.
	suspend func() (resume func())

	mu     sync.Mutex
	client *ipc.Client
}

func (b *backend) setClient(c *ipc.Client) {
	b.mu.Lock()
	b.client = c
	b.mu.Unlock()
}

func (b *backend) Send(cmd ipc.Command) {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	go func() {
		if c == nil {
			b.events <- resultMsg{cmd: cmd, err: ipc.ErrClosed}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		info, err := c.Do(ctx, cmd)
		b.events <- resultMsg{cmd, info, err}
	}()
}

// Open starts the browser in the background; a failure of the browser
// command shows up as an error message.
func (b *backend) Open(url string) error {
	go func() {
		if err := browser.OpenWait(b.opts.Config.Browser, url); err != nil {
			b.events <- resultMsg{err: err}
		}
	}()
	return nil
}

func (b *backend) Copy(text string) error { return browser.Copy(text, b.tty) }

// Edit runs the user's editor ($VISUAL, $EDITOR, else vi) on text in a
// temporary file, with the terminal handed over, and returns the file's
// content once the editor exits.
func (b *backend) Edit(text string) (string, error) {
	if b.suspend == nil {
		return text, errors.New("cannot open an editor: no controlling terminal")
	}
	f, err := os.CreateTemp("", "ghwatch-reply-*.md")
	if err != nil {
		return text, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return text, err
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	// Through the shell, as git does, so EDITOR may carry arguments.
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "ghwatch-editor", f.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	resume := b.suspend()
	err = cmd.Run()
	resume()
	if err != nil {
		return text, fmt.Errorf("editor %s: %w", editor, err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return text, err
	}
	return string(data), nil
}

// Run shows the TUI until the user quits.
func Run(ctx context.Context, opts Options) error {
	if opts.Config.Theme != "" {
		if err := UseTheme(opts.Config.Theme); err != nil {
			return err
		}
	}
	in, out := os.Stdin, os.Stdout
	fd := int(in.Fd())
	state, err := makeRaw(fd)
	if err != nil {
		return fmt.Errorf("the TUI needs a terminal: %w", err)
	}
	defer restore(fd, state)
	out.WriteString("\x1b[?1049h\x1b[?25l")
	defer out.WriteString("\x1b[?25h\x1b[?1049l")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan any, 64)
	be := &backend{opts: opts, events: events, tty: out}
	m := NewModel(be)
	m.Color = os.Getenv("NO_COLOR") == ""

	// Show the last known state at once, before any connection (T4).
	if snap, err := daemon.ReadSnapshot(opts.Paths.Snapshot()); err == nil {
		m.SetSnapshot(snap)
	}

	keys := newKeyReader(in)
	be.suspend = keys.suspender(fd, out, state)
	go keys.run(events)
	go connect(ctx, opts, be, events)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(sigs)
	go func() {
		for s := range sigs {
			if s == syscall.SIGWINCH {
				events <- resizeMsg{}
			} else {
				events <- quitMsg{}
			}
		}
	}()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case events <- tickMsg{}:
				default:
				}
			}
		}
	}()

	var frame bytes.Buffer
	draw := func() {
		w, h, err := termSize(fd)
		if err != nil || w <= 0 || h <= 0 {
			w, h = 80, 24
		}
		frame.Reset()
		frame.WriteString("\x1b[H")
		for i, row := range m.View(w, h) {
			fmt.Fprintf(&frame, "\x1b[%d;1H%s\x1b[0m\x1b[K", i+1, row)
		}
		out.Write(frame.Bytes())
	}
	for !m.Quit() {
		draw()
		select {
		case <-ctx.Done():
			return nil
		case ev := <-events:
			switch ev := ev.(type) {
			case keyMsg:
				m.Key(string(ev))
			case snapMsg:
				m.SetSnapshot(ev)
			case connMsg:
				m.SetConnected(bool(ev))
			case resultMsg:
				m.Done(ev.cmd, ev.info, ev.err)
			case quitMsg:
				return nil
			}
		}
	}
	return nil
}

// keyReader reads keys from the terminal and can pause, so an editor
// gets the keyboard to itself. It reads /dev/tty, which supports read
// deadlines: a deadline in the past interrupts a blocked read.
type keyReader struct {
	f        *os.File
	pausable bool
	paused   atomic.Bool
	parked   chan struct{}
	wake     chan struct{}
}

func newKeyReader(stdin *os.File) *keyReader {
	r := &keyReader{f: stdin, parked: make(chan struct{}), wake: make(chan struct{})}
	if tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0); err == nil {
		if tty.SetReadDeadline(time.Time{}) == nil {
			r.f, r.pausable = tty, true
		} else {
			tty.Close()
		}
	}
	return r
}

func (r *keyReader) run(events chan<- any) {
	buf := make([]byte, 256)
	for {
		n, err := r.f.Read(buf)
		if n > 0 && !r.paused.Load() {
			for _, k := range parseKeys(buf[:n]) {
				events <- keyMsg(k)
			}
		}
		switch {
		case err == nil:
		case errors.Is(err, os.ErrDeadlineExceeded) && r.paused.Load():
			r.parked <- struct{}{}
			<-r.wake
			r.f.SetReadDeadline(time.Time{})
		default:
			events <- quitMsg{}
			return
		}
	}
}

// pause stops reading keys; it returns once the reader is parked.
func (r *keyReader) pause() {
	r.paused.Store(true)
	r.f.SetReadDeadline(time.Now())
	<-r.parked
}

func (r *keyReader) resume() {
	r.paused.Store(false)
	r.wake <- struct{}{}
}

// suspender returns the backend's suspend: it parks the key reader and
// gives the terminal back in its normal mode (cooked, as it was before
// the TUI started), then undoes both. It is nil when keys cannot be
// paused.
func (r *keyReader) suspender(fd int, out *os.File, cooked *termState) func() func() {
	if !r.pausable {
		return nil
	}
	return func() func() {
		r.pause()
		out.WriteString("\x1b[?25h\x1b[?1049l")
		restore(fd, cooked)
		return func() {
			makeRaw(fd)
			out.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J")
			r.resume()
		}
	}
}

// connect keeps a connection to the daemon, reconnecting on its own. When
// no daemon runs and auto-start is on, it spawns one (at most every 30s).
func connect(ctx context.Context, opts Options, be *backend, events chan<- any) {
	var lastSpawn time.Time
	for ctx.Err() == nil {
		c, err := ipc.Dial(opts.Paths.Socket())
		if err != nil {
			if opts.Config.AutoStart && time.Since(lastSpawn) > 30*time.Second && !daemon.Running(opts.Paths.Lock()) {
				lastSpawn = time.Now()
				if err := daemon.Spawn(opts.Paths); err != nil {
					events <- resultMsg{err: fmt.Errorf("start daemon: %w", err)}
				} else {
					events <- resultMsg{info: "started the ghwatch daemon"}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		be.setClient(c)
		events <- connMsg(true)
		func() {
			for {
				select {
				case <-ctx.Done():
					c.Close()
					return
				case s, ok := <-c.Snapshots():
					if !ok {
						return
					}
					events <- snapMsg(s)
				}
			}
		}()
		be.setClient(nil)
		events <- connMsg(false)
	}
}
