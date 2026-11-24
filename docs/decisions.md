# Decisions

## Resolved open questions

| Question | Decision | Implementation |
| --- | --- | --- |
| Notification noise on re-runs | Notify per failing job as it fails | A re-run moves a check from failed to pending or running; failing again is a new transition. |
| Event types enabled when alerts are turned on | Everything | `model.DefaultSettings` enables all five types. |
| Alerts from the CLI | TUI only | There is no `alerts` subcommand. The socket command exists for the TUI. |
| macOS | Linux for now, keep it open | Terminal code builds on Linux and the BSDs/macOS. Notifications are behind `notify.Notifier`; the `exec` notifier works anywhere. |
| Auto-add own PRs | No; later revised: auto-add PRs that call for you | Own (authored) PRs are not added. `[autoadd]` in config.toml can add, each on its own, the open PRs requesting your review (from you, not your teams) and those assigned to you. Off by default; see "Auto-add" below. |
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

- **Look**: the layout follows jira-tabbed-tui: `N:name` tabs, lists in a
  bordered box with a column header, a full-width bar on the selected
  row, help and settings as centred panels. The colors are Catppuccin's,
  by role as its style guide suggests: Text and Subtext for content, Blue
  for the accent, Red/Green/Yellow for states, Mauve for merged, Sapphire
  for the merge queue, Peach for what is new, a Surface 1 bar for the
  selection, and a Mantle background with a Lavender border for panels.
  jira-tabbed-tui's own palette (`#555555` hints, `#5555ff` accent) was
  made for a black background and fell below 2:1 contrast on a dark blue
  one. Content is never drawn in Overlay or Surface colors, which are
  for decoration only; a test keeps every content color at 4.5:1 or more
  on the dark flavors' base. ghwatch does not paint the background, so
  the flavor (`theme`, Frappé by default) should match the terminal's.
  Colors are 24-bit; without colors (`NO_COLOR`) the active tab is
  bracketed and the selected row keeps a `›` marker.

- **Icon sets**: a terminal program can't ask whether a glyph will be drawn
  (a missing glyph still takes one cell), so the icons are a setting.
  `fancy` (default) uses Unicode symbols (`✗` `✓` `●` `◌` `–` `⊘`) that rely
  on the terminal borrowing glyphs from other fonts. `safe` uses only
  characters that common monospace fonts have (checked against DejaVu Sans
  Mono, Noto Sans Mono, Liberation Mono, Fira Code, JetBrains Mono, Source
  Code Pro and Ubuntu Mono): `×` `√` `*` `o` `–` `ø`. ASCII (`x` `v` `*` `o`
  `-` `/`) is chosen automatically in the two detectable cases, a non-UTF-8
  locale and the Linux console. PR states are letters in every set (`M`
  merged, `Q` queued, `C` closed). UI markers always use safe characters:
  `›` for the selection, `!` for stale/errors. Only the alert bell, and the comment
  mark with the fancy icons, are Nerd Font glyphs. A test in
  `internal/model` keeps the safe and ASCII sets to the allowed
  characters.

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
  GitHub charges a query by the connections it reads, and each review
  thread's comments are one: comments make a poll cost about 56 requests
  per PR instead of 3. With 20 PRs, that is about 11 points a poll.
- **Poll interval**: 30 minutes by default. PRs rarely change by the
  minute, and a check that just failed can wait; `p` polls at once when
  it cannot. At 30 minutes, 20 PRs cost about 22 points an hour, under
  0.5% of the budget of 5,000 (polling every minute cost some 13%).
- **Auto-add**: the daemon searches GitHub with each poll (and on `p`),
  one GraphQL request for both searches. A PR it adds goes into the
  watchlist with a `# auto: …` note, so the file stays the single list
  and can still be edited by hand. Removing an auto-added PR, with `d` or
  in the file, dismisses it for good (`dismissed` in the snapshot); adding
  it by hand lifts that. GitHub ends a review request both when you
  review and when you are removed from the reviewers, and the search
  cannot tell them apart; ghwatch can, from the reviews it fetches. So an
  auto-added PR goes away on its own when its review request ends without
  a review of yours, or when you are unassigned, and stays when you
  reviewed it, since that is when the author's answer matters. This is
  decided after the poll's fetch, so a review just submitted is seen. A
  PR removed this way is not dismissed: it comes back if it calls for you
  again. Reasons whose toggle is off are left alone, so turning a toggle
  off removes nothing. `max` (20) caps how many are watched at once, and
  `repos` (glob patterns on owner/repo, case ignored) limits the
  repositories; the search itself is not narrowed, the results are, so a
  repository dropped from the list sheds its auto-added PRs like any PR
  that stopped calling for you.

- **Comments**: what is new is decided locally, from when you last looked
  at the PR in ghwatch (`comments_read_at`). GitHub's own read state
  needs the notifications API and a token scope `gh` does not always
  have. Your own comments never count; bots count unless listed in
  `ignore_authors`. New comments stay marked for the whole visit of a
  tab, like the order of belled jobs, so the list does not change while
  you read it.
