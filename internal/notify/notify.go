// Package notify is the notifier extension point: it delivers a
// transition to the user, with an optional click action.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/clobrano/ghwatch/internal/model"
)

// Notification is one message to deliver.
type Notification struct {
	Title  string           `json:"title"`
	Body   string           `json:"body"`
	URL    string           `json:"url,omitempty"` // opened on click
	Urgent bool             `json:"urgent,omitempty"`
	Event  model.Transition `json:"event"`
}

// Notifier delivers notifications.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Opener opens a URL, used for click actions.
type Opener func(url string) error

// Nop discards notifications.
type Nop struct{}

// Notify implements Notifier.
func (Nop) Notify(context.Context, Notification) error { return nil }

// Multi sends to every notifier and joins their errors.
type Multi []Notifier

// Notify implements Notifier.
func (m Multi) Notify(ctx context.Context, n Notification) error {
	var errs []error
	for _, x := range m {
		errs = append(errs, x.Notify(ctx, n))
	}
	return errors.Join(errs...)
}

// Desktop sends freedesktop notifications through notify-send. When
// notify-send supports actions (libnotify 0.7.10+), clicking the
// notification opens its URL.
type Desktop struct {
	Open Opener
	// Timeout is how long to wait for a click before giving up.
	Timeout time.Duration

	once    sync.Once
	actions bool
	err     error
}

func (d *Desktop) probe() {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		d.err = fmt.Errorf("desktop notifications need notify-send (libnotify): %w", err)
		return
	}
	out, _ := exec.Command(path, "--help").CombinedOutput()
	d.actions = bytes.Contains(out, []byte("--action"))
}

// Notify implements Notifier. It returns once the notification is shown;
// waiting for a click happens in the background.
func (d *Desktop) Notify(ctx context.Context, n Notification) error {
	d.once.Do(d.probe)
	if d.err != nil {
		return d.err
	}
	args := []string{"--app-name=ghwatch"}
	if n.Urgent {
		args = append(args, "--urgency=critical")
	}
	if !d.actions || n.URL == "" || d.Open == nil {
		args = append(args, "--", n.Title, n.Body)
		return exec.CommandContext(ctx, "notify-send", args...).Run()
	}
	timeout := d.Timeout
	if timeout == 0 {
		timeout = time.Hour
	}
	args = append(args, "--action=default=Open", "--wait", "--", n.Title, n.Body)
	wctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(wctx, "notify-send", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	go func() {
		defer cancel()
		if cmd.Wait() == nil && strings.TrimSpace(out.String()) == "default" {
			_ = d.Open(n.URL)
		}
	}()
	return nil
}

// Exec is the plugin notifier: it runs a shell command with the
// notification as JSON on stdin, so any delivery channel (ntfy.sh, a chat
// webhook, a terminal bell) can be added without changing ghwatch.
type Exec struct {
	Command string
}

// Notify implements Notifier.
func (e Exec) Notify(ctx context.Context, n Notification) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", e.Command)
	cmd.Stdin = bytes.NewReader(data)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify command: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// Format builds the notification for a transition of item it.
func Format(t model.Transition, it model.Item) Notification {
	ref := fmt.Sprintf("%s#%d", it.Repo, it.Number)
	body := strings.TrimSpace(ref + " " + it.Title)
	n := Notification{Body: body, URL: t.URL, Event: t}
	if n.URL == "" {
		n.URL = it.URL
	}
	switch t.Type {
	case model.EventCheckFailed:
		n.Title = fmt.Sprintf("%s %s failed", model.Failed.Icon(), t.Check)
		n.Urgent = true
	case model.EventCheckStarted:
		n.Title = fmt.Sprintf("%s %s started", model.Running.Icon(), t.Check)
	case model.EventCheckFinished:
		n.Title = fmt.Sprintf("%s %s %s", t.To.Icon(), t.Check, t.To)
	case model.EventAllPassed:
		n.Title = fmt.Sprintf("%s all checks passed", model.Passed.Icon())
	case model.EventRestarted:
		n.Title = fmt.Sprintf("%s CI restarted by a new push", model.Pending.Icon())
		if len(it.HeadSHA) >= 7 {
			n.Title += " (" + it.HeadSHA[:7] + ")"
		}
	case model.EventFinished:
		n.Title = fmt.Sprintf("%s %s", t.To.Icon(), string(it.Lifecycle))
	default:
		n.Title = string(t.Type)
	}
	return n
}
