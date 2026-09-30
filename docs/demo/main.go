// Command demo runs a ghwatch daemon on a scripted, fake GitHub, to record
// the README demo (see demo.tape). It uses the real daemon and protocol;
// only the pull requests come from a script instead of the GitHub API, so
// every recording tells the same story:
//
//   - #123: jobs finish one by one, then e2e-aws-ovn fails;
//   - #131: all green, enters the merge queue, moves up, and merges;
//   - osac#58 and #140: settled.
//
// Run it with XDG_CONFIG_HOME, XDG_STATE_HOME and XDG_RUNTIME_DIR pointing
// to a scratch directory, so it does not touch a real daemon or watchlist.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/clobrano/ghwatch/internal/config"
	"github.com/clobrano/ghwatch/internal/daemon"
	"github.com/clobrano/ghwatch/internal/github"
	"github.com/clobrano/ghwatch/internal/kind"
	"github.com/clobrano/ghwatch/internal/kind/pr"
	"github.com/clobrano/ghwatch/internal/model"
	"github.com/clobrano/ghwatch/internal/notify"
)

var watchlist = []string{
	"medik8s/node-healthcheck-operator#123",
	"medik8s/node-healthcheck-operator#131",
	"medik8s/node-healthcheck-operator#140",
	"osac-project/osac#58",
}

func main() {
	speed := flag.Float64("speed", 1, "play the story faster (>1) or slower (<1)")
	flag.Parse()
	paths := config.DefaultPaths()
	if err := paths.Ensure(); err != nil {
		log.Fatal(err)
	}
	os.Remove(paths.Snapshot()) // start the story from scratch
	if err := os.WriteFile(paths.Watchlist(), []byte(strings.Join(watchlist, "\n")+"\n"), 0o600); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := &daemon.Daemon{
		Paths:    paths,
		Config:   config.Config{Interval: time.Second},
		Kinds:    kind.NewRegistry(&story{start: time.Now(), speed: *speed}),
		Notifier: notify.Nop{},
		Log:      log.New(os.Stderr, "demo: ", 0),
	}
	if err := d.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// story is a pull-request kind whose state follows a script over time.
type story struct {
	pr.Kind
	start time.Time
	speed float64
}

// Fetch implements kind.Kind.
func (s *story) Fetch(_ context.Context, _ *github.Client, ids []string) ([]model.Item, *github.RateLimit, error) {
	now := time.Now()
	t := time.Duration(float64(now.Sub(s.start)) * s.speed) // story time
	ago := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	check := func(name, source string, st model.State, started, took time.Duration) model.Check {
		c := model.Check{Name: name, Source: source, State: st, Required: name != "tide",
			URL: "https://prow.ci.openshift.org/view/" + name}
		if source == pr.SourceActions {
			c.URL = "https://github.com/medik8s/node-healthcheck-operator/actions/runs/1/job/" + name
		}
		if st != model.Pending {
			c.StartedAt = ago(started)
		}
		if st.Done() {
			c.CompletedAt = ago(started - took)
		}
		return c
	}
	after := func(sec int, then, before model.State) model.State {
		if t >= time.Duration(sec)*time.Second {
			return then
		}
		return before
	}

	var out []model.Item
	for _, id := range ids {
		it := s.Stub(id)
		it.Lifecycle, it.Author = model.Open, "clobrano"
		switch it.Number {
		case 123:
			it.Title, it.Branch, it.HeadSHA = "Fix lease renewal race", "fix-lease-race", "a1b2c3d4e5f6"
			it.PushedAt = *ago(52 * time.Minute)
			it.Checks = []model.Check{
				check("lint", pr.SourceActions, model.Passed, 50*time.Minute, 2*time.Minute),
				check("unit", pr.SourceActions, after(4, model.Passed, model.Running), 50*time.Minute, 6*time.Minute),
				check("e2e-aws-ovn", pr.SourceProw, after(9, model.Failed, model.Running), 48*time.Minute, 41*time.Minute),
				check("e2e-metal-ipi", pr.SourceProw, after(6, model.Running, model.Pending), 23*time.Minute, 0),
				check("ci/prow/images", pr.SourceProw, after(3, model.Passed, model.Running), 49*time.Minute, 12*time.Minute),
				check("tide", pr.SourceProw, model.Pending, 0, 0),
			}
		case 131:
			it.Title, it.Branch, it.HeadSHA = "Raise SBD watchdog timeout", "sbd-timeout", "b7c8d9e0f1a2"
			it.PushedAt = *ago(3 * time.Hour)
			it.Checks = []model.Check{
				check("lint", pr.SourceActions, model.Passed, 3*time.Hour, 2*time.Minute),
				check("unit", pr.SourceActions, model.Passed, 3*time.Hour, 7*time.Minute),
				check("e2e-aws-ovn", pr.SourceProw, model.Passed, 170*time.Minute, 44*time.Minute),
			}
			q := &model.MergeQueue{URL: "https://github.com/medik8s/node-healthcheck-operator/queue/main"}
			switch {
			case t >= 30*time.Second:
				it.Lifecycle, q = model.LifeMerged, nil
			case t >= 20*time.Second:
				q.State, q.Position, q.EnqueuedAt, q.ETASeconds = "awaiting_checks", 1, *ago(9 * time.Minute), 240
			case t >= 12*time.Second:
				q.State, q.Position, q.EnqueuedAt, q.ETASeconds = "queued", 2, *ago(5 * time.Minute), 780
			default:
				q = nil
			}
			it.MergeQueue = q
		case 140:
			it.Title, it.Branch, it.HeadSHA = "Document remediation templates", "docs-templates", "c3d4e5f6a7b8"
			it.PushedAt, it.Lifecycle = *ago(26 * time.Hour), model.LifeMerged
			it.Checks = []model.Check{check("lint", pr.SourceActions, model.Passed, 26*time.Hour, time.Minute)}
		case 58:
			it.Title, it.Branch, it.HeadSHA = "Add bare metal pool quota", "bm-quota", "d4e5f6a7b8c9"
			it.Author, it.PushedAt = "danmanor", *ago(5 * time.Hour)
			it.Checks = []model.Check{
				check("build", pr.SourceActions, model.Passed, 5*time.Hour, 4*time.Minute),
				check("test", pr.SourceActions, model.Passed, 5*time.Hour, 9*time.Minute),
			}
		}
		it.State = model.ItemState(it)
		it.UpdatedAt = now
		out = append(out, it)
	}
	return out, nil, nil
}
