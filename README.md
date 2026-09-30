# ghwatch

ghwatch watches a hand-picked set of GitHub pull requests and shows the state
of every CI check (pending, running, passed, failed, skipped or cancelled)
for GitHub Actions check-runs and for external commit statuses such as
Prow / OpenShift CI. A single background daemon polls GitHub. Any number of
tabbed TUIs and tmux status lines read its state, and it can send a desktop
notification when a check changes state. Notifications are opt-in per PR.

```
 ✗ #123 lease-race ⍾ │ ● #131 sbd-timeout │ ✓ #140 docs │ ● osac#58
─────────────────────────────────────────────────────────────────────
 org/repo#123  Fix lease renewal race                    @clobrano
 head a1b2c3d · pushed 52m ago · 5/7 done · 1 failing · alerts on
─────────────────────────────────────────────────────────────────────
 Failed · 1
 ✗ e2e-aws-ovn                      41m    Prow
 Running · 3
▸● e2e-metal-ipi                running 23m Prow
 ● ci/prow/images               running 5m  Prow
 ◌ tide                             pending Prow     opt
 Passed · 2
 ✓ lint                              2m    Actions
 ✓ unit                              6m    Actions
─────────────────────────────────────────────────────────────────────
 h/l tab · j/k check · enter job · o PR · n alerts · N events · ?   connected
```

## Install

```sh
go install github.com/clobrano/ghwatch/cmd/ghwatch@latest
```

ghwatch is a single static binary built only on the Go standard library.
At runtime it needs:

- a GitHub token, read from `$GITHUB_TOKEN`, `$GH_TOKEN` or `gh auth token`
  (ghwatch never writes it to disk);
- `notify-send` (libnotify) for desktop notifications. With libnotify
  0.7.10 or later, clicking a notification opens the job page;
- `xdg-open`, or `$BROWSER`, to open links.

Linux is the primary platform. The code also builds on macOS and the BSDs,
but notifications there need the `exec` notifier (see below).

## Usage

```sh
ghwatch add https://github.com/org/repo/pull/123   # or: ghwatch add org/repo#123
ghwatch ls                                          # list watched PRs
ghwatch rm org/repo#123
ghwatch checks org/repo#123                         # fetch and print checks now
ghwatch                                             # open the TUI
ghwatch -serve                                      # run the daemon in the foreground
ghwatch status                                      # one line, e.g. "PR ●3 ✓5 ✗1"
ghwatch status -json                                # the full state snapshot
```

The TUI starts the daemon in the background if none is running. A daemon
started this way exits 10 seconds after its last TUI quits. So quitting
every TUI stops it, and after a rebuild the next TUI starts the new binary.
While no daemon runs, nothing is polled, no notifications are sent, and
`ghwatch status` shows the state as stale.

To get notifications and a live status line with no TUI open, run the
daemon yourself, in a tmux pane or under a service manager:
`ghwatch -serve` keeps running until you stop it with Ctrl-C or SIGTERM.
Add `-idle-exit 10m` to make it exit after that long without clients. Only
one daemon runs per user. A second one detects the first and exits.

`add` and `rm` go through the daemon when it is running, so every open TUI
updates at once. Otherwise they edit the watchlist file directly.

### tmux

`ghwatch status` reads the snapshot file and makes no network call, so it is
cheap to run from every session:

```tmux
set -g status-right '#(ghwatch status) %H:%M'
set -g status-interval 10
```

A trailing `⚠` means the state is stale: the daemon is not running, or its
last poll failed.

### TUI keys

| Key | Action |
| --- | --- |
| `h` / `l`, `gT` / `gt` | previous / next tab |
| `1`–`9` | jump to tab N |
| `/` | find a PR by number, title or branch |
| `j` / `k`, `gg` / `G` | move between checks |
| `enter` | open the selected check's job page |
| `o` | open the PR page |
| `y` | copy the selected check's URL |
| `n` | toggle notifications for the current PR |
| `b` | toggle notifications for the selected job |
| `N` | notification settings: event types and global mute |
| `a` | add a PR |
| `d` | unwatch the current PR (in every client) |
| `r` | ask the daemon to poll now |
| `R` | re-run failed CI: comment `/retest` for Prow, re-run failed Actions jobs |
| `?` | help |
| `q` | quit this TUI (the daemon keeps running) |

Tabs keep the order in which the PRs were added. Checks are split into
groups, each under a header with its count: **Failed**, then **Running**
(including pending), then **Passed** (including skipped), then
**Cancelled** at the bottom. Within a group, checks stay in the order they were
first seen. When a check changes group, the selection follows it. A tab label starts with the PR's state icon, and a
`⍾` marks PRs with alerts on. A PR from a repository other than the most
common one gets a short repository prefix (`osac#58`). Checks marked `opt`
are not required by branch protection. When a PR has required checks, a
failing optional check does not turn the PR red.

### Notifications

Notifications are off for every PR until you press `n` on its tab, or `b`
on one of its jobs. The
setting is shared by all clients and survives restarts. `N` chooses which
events notify: check failed, all checks passed, check started, CI restarted
by a new push, merged or closed. All of them are enabled at first.

To follow a single job, select it and press `b`. The job shows a `⍾` after
its name, and the tab and header show a bell too. That job then notifies
whenever its state changes (started, failed, passed, skipped, cancelled),
even with the PR's alerts off. The bell stays on the job across new pushes,
since a re-run job keeps its name. When an event is wanted both for the
PR and for the job, it still sends one notification. The global mute
silences job bells too. Each
event sends exactly one notification, from the daemon, however many clients
are open. After a `/retest`, a notification is sent for each job as it
fails.

## Configuration

Everything lives in plain files:

| Path | Contents |
| --- | --- |
| `$XDG_CONFIG_HOME/ghwatch/watch` | the watchlist: one PR per line, `#` comments; edit freely, the daemon reloads it |
| `$XDG_CONFIG_HOME/ghwatch/config.toml` | settings, below |
| `$XDG_STATE_HOME/ghwatch/snapshot.json` | the state snapshot, including notification settings ([schema](docs/snapshot.md)) |
| `$XDG_STATE_HOME/ghwatch/daemon.log` | output of an auto-started daemon |
| `$XDG_RUNTIME_DIR/ghwatch/ghwatch.sock` | the daemon socket (mode 0600) |

```toml
interval = "60s"            # poll interval (minimum 10s)
browser = "firefox"         # default: $BROWSER, then xdg-open; "%s" is replaced by the URL
status_template = 'PR {{if .Running}}●{{.Running}} {{end}}{{if .Passed}}✓{{.Passed}} {{end}}{{if .Failed}}✗{{.Failed}} {{end}}{{if .Stale}}⚠{{end}}'

[notify]
backend = "desktop"         # desktop | exec | none
# command = "curl -s -H \"Title: $(jq -r .title)\" -d @- ntfy.sh/my-topic"

[daemon]
autostart = true            # clients start the daemon when it is not running
```

The status template is a Go `text/template`. It can use `.Running`,
`.Pending`, `.Passed`, `.Failed`, `.Merged`, `.Closed`, `.Total`, `.Stale`
and `.Items`. Pending PRs count as running in the default template.

The `exec` notifier is the plugin hook for other delivery channels. It runs
`command` with `sh -c` and writes the notification as JSON on stdin:
`title`, `body`, `url`, `urgent`, and `event` (`type`, `item_id`, `check`,
`from`, `to`, `url`).

## How it works

```
 GitHub GraphQL ──(1 batched request / poll)──▶ daemon ──▶ desktop notifications
                                                  │
                     snapshot.json ◀──────────────┤
                          │                       │ Unix socket, NDJSON
               ghwatch status (tmux)        TUI, TUI, … (any tmux session / terminal)
```

- The daemon polls only the PRs on the watchlist. All of them go into one
  GraphQL request per cycle. Each request fetches the head commit's
  check-runs and commit statuses and whether each check is required. PRs
  that are merged or closed are no longer fetched.
- When a check is re-run, only its latest run is kept. A new push resets
  the PR to the new head's checks.
- Polling backs off exponentially on errors (up to 15 minutes). When less
  than 5% of the rate limit is left, polling waits for the reset. Errors
  show as `stale` in every client, and the daemon never exits on them.
- Clients get the full snapshot on connect, then every update. Commands
  (add, unwatch, poll, alerts, event types, mute, retest) change the shared
  state, which is broadcast to every client. Each client has a bounded
  queue. A client that falls behind is dropped and reconnects with a fresh
  snapshot, so it never slows the others.
- A TUI shows the last saved snapshot at once, without a network call.
  When the daemon goes away, the TUI shows `disconnected` and reconnects on
  its own.

### Code layout

| Path | Contents |
| --- | --- |
| `cmd/ghwatch` | entry point and CLI subcommands |
| `internal/model` | items, checks, states, transitions, snapshot schema |
| `internal/kind` | item-kind interface and registry; `kind/pr` is the pull-request kind and its check providers |
| `internal/github` | token lookup and a small GraphQL/REST client |
| `internal/daemon` | poll loop, differ, shared state, watchlist reload, snapshot writer, single-instance lock |
| `internal/ipc` | Unix socket server/client, NDJSON messages, per-client queues |
| `internal/notify` | notifier interface; desktop (notify-send) and exec implementations |
| `internal/tui` | tabbed TUI (raw terminal, no third-party dependency) |
| `internal/watchlist`, `internal/config`, `internal/browser` | files, settings, links and clipboard |

### Extending

Four extension points are Go interfaces with implementations registered at
compile time. None of them touches the poll, diff and notify loop:

- **Item kind** (`kind.Kind`): parse input into a namespaced ID (`pr:org/repo#1`),
  fetch a batch, and build a placeholder. Kinds may also implement `kind.Retester`.
- **Check provider** (`pr.Provider`): turn one raw rollup context into a check.
- **Notifier** (`notify.Notifier`): deliver a notification.
- **View**: read the [snapshot](docs/snapshot.md), a versioned public
  contract, from the file or the socket.

See [docs/decisions.md](docs/decisions.md) for the resolved product
questions and the implementation choices.
