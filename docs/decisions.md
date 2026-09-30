# Decisions

## Resolved open questions

| Question | Decision | Implementation |
| --- | --- | --- |
| Notification noise on re-runs | Notify per failing job as it fails | A re-run moves a check from failed to pending or running; failing again is a new transition. |
| Event types enabled when alerts are turned on | Everything | `model.DefaultSettings` enables all five types. |
| Alerts from the CLI | TUI only | There is no `alerts` subcommand. The socket command exists for the TUI. |
| macOS | Linux for now, keep it open | Terminal code builds on Linux and the BSDs/macOS. Notifications are behind `notify.Notifier`; the `exec` notifier works anywhere. |
| Auto-add own PRs | No | The watchlist stays curated by hand. |
| Re-triggering CI | Moved into scope (M2) | `R` in the TUI: comments `/retest` when a failed check comes from Prow, and calls `rerun-failed-jobs` for each failed Actions workflow run. |
| Required vs optional checks | Distinguish them | GraphQL `isRequired` per check. When any check is required, only required checks decide the PR state. Optional checks show `opt`. |
| External plugins | Yes, gh-extension style | Notifiers: the `exec` notifier pipes JSON on stdin to any command. Item kinds as external executables are left for later; the kind interface and snapshot contract are the seams. |
| Second item kind | Out of scope | — |

## Standard library instead of third-party modules

The PRD names Bubble Tea, Bubbles, Lip Gloss, godbus, fsnotify and a TOML
parser. This first version uses only the standard library, for these parts:

- **TUI**: raw-mode terminal handling (`termios` via `syscall`), key
  parsing and ANSI rendering in `internal/tui`. The model/view is separate
  from the terminal loop, so it is unit-tested without a terminal and could
  move to Bubble Tea later without changing the rest of the program.
- **Notifications**: `notify-send` instead of D-Bus through godbus. It uses
  the same freedesktop service, and click actions work through
  `--action`/`--wait` (libnotify 0.7.10+).
- **Watchlist reload**: the daemon stats the file every 2 seconds instead
  of using inotify. This is cheap, and it also works when editors replace
  the file.
- **config.toml**: a small parser for the subset ghwatch needs: tables,
  and string, integer and boolean values.

The result is one static binary with no module dependencies, in line with
the "dependency-light" goal. Each part sits behind a small interface and
can be swapped out.

## Other choices

- **Daemon lifetime**: a daemon auto-started by a TUI exits 10 seconds
  after its last client disconnects (`-idle-exit 10s`). This keeps a
  stale daemon from outliving a rebuild. It narrows goal 4
  (notifications without the TUI open) to daemons started by hand:
  `ghwatch -serve` without `-idle-exit` runs until stopped. The tmux
  status line reads the state file and holds no connection, so it does
  not keep the daemon alive.

- **Dismissing a finished PR** is unwatching it (`d`). A merged or closed
  PR stays on the list with its final state until then, and is no longer
  polled.
- **"Pushed at"** is the head commit's commit date. GitHub's GraphQL API no
  longer exposes the push time.
- **Check timestamps for commit statuses**: a status only carries the time
  of its latest state. The daemon takes the start time from earlier polls,
  so a status's duration is exact when ghwatch saw it running.
- **Rate limit**: one GraphQL query per poll (up to 40 PRs per request).
  With 20 PRs every 60 s, that is well under 5% of the hourly budget.
