// Command ghwatch watches a hand-picked set of GitHub pull requests and
// reports the state of every CI check.
//
//	ghwatch                  open the tabbed TUI
//	ghwatch -serve           run the daemon in the foreground
//	ghwatch add <PR>...      watch PRs (URL or owner/repo#number)
//	ghwatch rm <PR>...       stop watching PRs
//	ghwatch ls               list watched PRs
//	ghwatch status           one-line summary for the tmux status bar
//	ghwatch checks <PR>      print the checks of a PR
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/clobrano/ghwatch/internal/browser"
	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/daemon"
	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/ipc"
	"github.com/clobrano/ghwatch/internal/kind"
	"github.com/clobrano/ghwatch/internal/kind/pr"
	"github.com/clobrano/ghwatch/internal/model"
	"github.com/clobrano/ghwatch/internal/notify"
	"github.com/clobrano/ghwatch/internal/tui"
	"github.com/clobrano/ghwatch/internal/watchlist"
)

const usage = `Usage:
  ghwatch                     open the tabbed TUI
  ghwatch -serve              run the daemon in the foreground
  ghwatch add <PR>...         watch PRs (URL or owner/repo#number)
  ghwatch rm <PR>...          stop watching PRs
  ghwatch ls                  list watched PRs
  ghwatch status [-json]      one-line summary, e.g. for tmux status-right
  ghwatch checks <PR>         print the checks of a PR, fetched now

Flags:
`

// kinds registers the item kinds compiled into ghwatch.
func kinds() *kind.Registry { return kind.NewRegistry(pr.New()) }

func main() {
	log.SetFlags(0)
	log.SetPrefix("ghwatch: ")
	serve := flag.Bool("serve", false, "run the daemon in the foreground")
	interval := flag.Duration("interval", 0, "poll interval for -serve (default from config, 60s)")
	idleExit := flag.Duration("idle-exit", 0, "with -serve: exit after this long without clients (0: never)")
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	paths := config.DefaultPaths()
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		log.Fatal(err)
	}
	if *interval > 0 {
		cfg.Interval = *interval
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *serve {
		if err := runDaemon(ctx, paths, cfg, *idleExit); err != nil {
			log.Fatal(err)
		}
		return
	}
	args := flag.Args()
	if len(args) == 0 {
		stop() // the TUI reads ctrl-c as a key
		if err := tui.Run(context.Background(), tui.Options{Paths: paths, Config: cfg}); err != nil {
			log.Fatal(err)
		}
		return
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "add":
		err = cmdAdd(ctx, paths, args)
	case "rm", "remove":
		err = cmdRemove(ctx, paths, args)
	case "ls", "list":
		err = cmdList(paths, os.Stdout)
	case "status":
		err = cmdStatus(paths, cfg, args, os.Stdout)
	case "checks":
		err = cmdChecks(ctx, args, os.Stdout)
	case "help":
		flag.Usage()
	default:
		err = fmt.Errorf("unknown command %q (see ghwatch -h)", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func runDaemon(ctx context.Context, paths config.Paths, cfg config.Config, idleExit time.Duration) error {
	d := &daemon.Daemon{
		Paths:    paths,
		Config:   cfg,
		IdleExit: idleExit,
		Kinds:    kinds(),
		GH:       github.New(),
		Log:      log.New(os.Stderr, "ghwatch: ", log.LstdFlags),
	}
	switch cfg.Notifier {
	case "desktop", "":
		d.Notifier = &notify.Desktop{Open: func(url string) error { return browser.Open(cfg.Browser, url) }}
	case "exec":
		if cfg.NotifyCommand == "" {
			return errors.New(`notifier "exec" needs notify_command in config.toml`)
		}
		d.Notifier = notify.Exec{Command: cfg.NotifyCommand}
	case "none":
		d.Notifier = notify.Nop{}
	default:
		return fmt.Errorf("unknown notifier %q (want desktop, exec or none)", cfg.Notifier)
	}
	if _, err := github.Token(); err != nil {
		d.Log.Printf("warning: %v", err)
	}
	return d.Run(ctx)
}

// dial connects to a running daemon, or returns nil.
func dial(paths config.Paths) *ipc.Client {
	c, err := ipc.Dial(paths.Socket())
	if err != nil {
		return nil
	}
	return c
}

func parser(reg *kind.Registry) watchlist.Parser {
	return func(s string) (string, error) {
		_, id, err := reg.Parse(s)
		return id, err
	}
}

func cmdAdd(ctx context.Context, paths config.Paths, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ghwatch add <PR URL | owner/repo#number>...")
	}
	reg := kinds()
	if c := dial(paths); c != nil {
		defer c.Close()
		var errs []error
		for _, a := range args {
			info, err := c.Do(ctx, ipc.Command{Op: ipc.OpAdd, ID: a})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			fmt.Println(info)
		}
		return errors.Join(errs...)
	}
	// No daemon: validate against GitHub and edit the file directly.
	if err := paths.Ensure(); err != nil {
		return err
	}
	gh := github.New()
	var errs []error
	for _, a := range args {
		k, id, err := reg.Parse(a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		items, _, err := k.Fetch(ctx, gh, []string{id})
		if err == nil && (len(items) != 1 || items[0].Error != "") {
			msg := "not found"
			if len(items) == 1 {
				msg = items[0].Error
			}
			err = fmt.Errorf("%s: %s", id, msg)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		changed, err := watchlist.Add(paths.Watchlist(), id, parser(reg))
		switch {
		case err != nil:
			errs = append(errs, err)
		case changed:
			fmt.Println("watching", id)
		default:
			fmt.Println("already watching", id)
		}
	}
	return errors.Join(errs...)
}

func cmdRemove(ctx context.Context, paths config.Paths, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ghwatch rm <PR URL | owner/repo#number>...")
	}
	reg := kinds()
	c := dial(paths)
	if c != nil {
		defer c.Close()
	}
	var errs []error
	for _, a := range args {
		_, id, err := reg.Parse(a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if c != nil {
			info, err := c.Do(ctx, ipc.Command{Op: ipc.OpUnwatch, ID: id})
			if err != nil {
				errs = append(errs, err)
			} else {
				fmt.Println(info)
			}
			continue
		}
		changed, err := watchlist.Remove(paths.Watchlist(), id, parser(reg))
		switch {
		case err != nil:
			errs = append(errs, err)
		case !changed:
			errs = append(errs, fmt.Errorf("not watching %s", id))
		default:
			fmt.Println("unwatched", id)
		}
	}
	return errors.Join(errs...)
}

func cmdList(paths config.Paths, w io.Writer) error {
	ids, bad, err := watchlist.Load(paths.Watchlist(), parser(kinds()))
	if err != nil {
		return err
	}
	for _, e := range bad {
		log.Print(e)
	}
	snap, _ := daemon.ReadSnapshot(paths.Snapshot())
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, id := range ids {
		var it *model.Item
		if snap != nil {
			it = snap.Find(id)
		}
		if it == nil || it.HeadSHA == "" {
			fmt.Fprintf(tw, "%s\t%s\t\n", id, "?")
			continue
		}
		st := model.ItemState(*it)
		fmt.Fprintf(tw, "%s\t%s %s\t%s\n", id, st.Icon(), st, it.Title)
	}
	return tw.Flush()
}

// StatusData is the data available to the status template.
type StatusData struct {
	Running, Pending, Passed, Failed, Queued, Merged, Closed, Total int
	Stale                                                           bool
	Items                                                           []model.Item
}

func cmdStatus(paths config.Paths, cfg config.Config, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the whole snapshot as JSON")
	format := fs.String("format", "", "text/template for the summary (default from config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asJSON {
		data, err := os.ReadFile(paths.Snapshot())
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	tmpl := cfg.StatusTemplate
	if *format != "" {
		tmpl = *format
	}
	t, err := template.New("status").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("status template: %w", err)
	}
	var data StatusData
	snap, err := daemon.ReadSnapshot(paths.Snapshot())
	if err != nil {
		data.Stale = true
	} else {
		data = summarize(snap)
		data.Stale = snap.Stale || time.Since(snap.GeneratedAt) > 3*cfg.Interval+time.Minute ||
			!daemon.Running(paths.Lock())
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, strings.TrimSpace(b.String()))
	return err
}

func summarize(snap *model.Snapshot) StatusData {
	var d StatusData
	d.Items = snap.Items
	for _, it := range snap.Items {
		d.Total++
		switch model.ItemState(it) {
		case model.Running:
			d.Running++
		case model.Pending:
			d.Pending++
			d.Running++ // not finished yet: counted as running in the summary
		case model.Passed:
			d.Passed++
		case model.Failed:
			d.Failed++
		case model.Queued:
			d.Queued++
		case model.Merged:
			d.Merged++
		case model.Closed:
			d.Closed++
		}
	}
	return d
}

func cmdChecks(ctx context.Context, args []string, w io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: ghwatch checks <PR URL | owner/repo#number>")
	}
	k, id, err := kinds().Parse(args[0])
	if err != nil {
		return err
	}
	items, _, err := k.Fetch(ctx, github.New(), []string{id})
	if err != nil {
		return err
	}
	if len(items) != 1 {
		return fmt.Errorf("%s: not found", id)
	}
	if items[0].Error != "" {
		return fmt.Errorf("%s: %s", id, items[0].Error)
	}
	printChecks(w, items[0], time.Now())
	return nil
}

func printChecks(w io.Writer, it model.Item, now time.Time) {
	st := model.ItemState(it)
	done, failed := model.Progress(it.Checks)
	fmt.Fprintf(w, "%s %s#%d %s\n", st.Icon(), it.Repo, it.Number, it.Title)
	fmt.Fprintf(w, "head %.7s · %d/%d done · %d failing\n", it.HeadSHA, done, len(it.Checks), failed)
	if len(it.Labels) > 0 {
		names := make([]string, len(it.Labels))
		for i, l := range it.Labels {
			names[i] = l.Name
		}
		fmt.Fprintf(w, "labels: %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, c := range it.Checks {
		var dur string
		switch {
		case c.StartedAt != nil && c.CompletedAt != nil && !c.CompletedAt.Before(*c.StartedAt):
			// Skipped checks may end "before" they start: no duration then.
			dur = c.CompletedAt.Sub(*c.StartedAt).Round(time.Second).String()
		case c.StartedAt != nil && c.State == model.Running:
			dur = now.Sub(*c.StartedAt).Round(time.Second).String()
		}
		req := ""
		if c.Required {
			req = "required"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\t%s\t%s\n", c.State.Icon(), c.Name, c.State, dur, c.Source, req, c.URL)
	}
	tw.Flush()
}
