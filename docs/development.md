# Developing ghwatch

The [README](../README.md) covers everyday use. This page has the
reference details and how ghwatch works inside.

## Reference

### The daemon

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

With `[daemon] autostart = false` in `config.toml`, TUIs don't start the
daemon; run `ghwatch -serve` yourself.

### Files

Everything lives in plain files:

| Path | Contents |
| --- | --- |
| `$XDG_CONFIG_HOME/ghwatch/watch` | the watchlist: one PR per line, `#` comments; edit freely, the daemon reloads it |
| `$XDG_CONFIG_HOME/ghwatch/config.toml` | settings (see the README) |
| `$XDG_STATE_HOME/ghwatch/snapshot.json` | the state snapshot, including notification settings ([schema](snapshot.md)) |
| `$XDG_STATE_HOME/ghwatch/daemon.log` | output of an auto-started daemon |
| `$XDG_RUNTIME_DIR/ghwatch/ghwatch.sock` | the daemon socket (mode 0600) |

### Icon sets

**Icons.** `fancy` uses Unicode symbols (`✗` `✓` `●` `◌` `–` `⊘`) that
many fonts lack: they show when the terminal borrows glyphs from other
fonts, as most Linux terminals do. If they show as boxes, set `icons =
"safe"` for characters every common monospace font has (`×` `√` `*` `o`
`–` `ø`). With a non-UTF-8 locale or on the Linux console, ghwatch uses
plain ASCII (`x` `v` `*` `o` `-` `/`) whatever the setting. PR states are
letters in every set: `M` merged, `Q` queued, `C` closed. Run `ghwatch
icons` to see all the sets in your terminal.

### Exec notifier

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
  check-runs and commit statuses, whether each check is required, the
  PR's labels and its merge queue entry. A merged or closed PR is fetched
  once after the daemon starts, then no more.
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

## Demo recording

`docs/demo.gif` is recorded with [VHS](https://github.com/charmbracelet/vhs)
from `docs/demo/demo.tape`. The tape builds ghwatch and a demo daemon
(`docs/demo/main.go`): the real daemon with a scripted PR source instead of
the GitHub API. Every recording then tells the same story, needs no
network access, and leaves your watchlist and running daemon alone. To
re-record after a UI change:

```sh
vhs docs/demo/demo.tape
```

This needs VHS 0.8 or later (with `ttyd` and `ffmpeg`), and the Nerd Font
named in the tape (JetBrainsMono Nerd Font by default).

## Code layout

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

## Extending

Four extension points are Go interfaces with implementations registered at
compile time. None of them touches the poll, diff and notify loop:

- **Item kind** (`kind.Kind`): parse input into a namespaced ID (`pr:org/repo#1`),
  fetch a batch, and build a placeholder. Kinds may also implement `kind.Retester`.
- **Check provider** (`pr.Provider`): turn one raw rollup context into a check.
- **Notifier** (`notify.Notifier`): deliver a notification.
- **View**: read the [snapshot](snapshot.md), a versioned public
  contract, from the file or the socket.

See [decisions.md](decisions.md) for the resolved product
questions and the implementation choices.
