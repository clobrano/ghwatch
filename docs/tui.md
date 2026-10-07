# The ghwatch TUI

Run `ghwatch` to open it. The screen follows the style of
[jira-tabbed-tui](https://github.com/clobrano/jira-tabbed-tui), from top to
bottom:

- **Title bar:** the app name and how many PRs are watched and in which
  state (failed, running, passed, queued, merged or closed). It also shows
  how many PRs have alerts, how many changed since you last looked
  (`•2 new`), whether notifications are muted, and when
  GitHub was last polled. The poll time turns yellow ("last poll … ago")
  while polling fails.
- **Tabs:** one per PR, in the order they were added. A label is the
  tab's number (its `1`–`9` key), the PR's state icon, the number and a
  short name, and a bell when alerts are on. A PR from a repository other
  than the most common one gets a short repository prefix (`osac#58`).
  The active tab is highlighted. An orange `•` marks a PR that changed
  since you last looked at it: its state changed (say, running to failed),
  it got a new push, or it was merged or closed. Pressing any key while
  on its tab clears the mark, in every open TUI.
- **PR header:** reference, title and author, then head commit, push
  time, progress and failures. The PR's labels show below as chips in
  their GitHub colors.
- **Job list:** a box with a Check / Time / Source header and the
  selected job highlighted. Jobs are split into groups, each under a
  header with its count: **Failed**, then **Running** (including
  pending), then **Passed** (including skipped), then **Cancelled** at
  the bottom. Within a group, jobs with a bell (`b`) come first; the
  others stay in the order they were first seen. When a job changes
  group, the selection follows it. Jobs marked `opt` are not required
  by branch protection. When a PR has required checks, a failing
  optional one does not turn the PR red.
- **Footer:** what the main keys do (`h/l prev/next PR · j/k next/prev job
  · enter open job · o open PR · …`), and the connection to the daemon.
  On narrow terminals the less important hints give way, and
  `? all keys` always stays. Messages replace the hints for a few
  seconds.

`?` opens the full list of keys in a panel, grouped by topic, and `N`
opens the notification settings the same way.

Links open where GitHub's PR page points: a GitHub Actions job opens its
job page, a check from another app (such as Konflux) opens its page on
GitHub, and a commit status (such as Prow) opens its target URL.

## Keys

| Key | Action |
| --- | --- |
| `h` / `l`, `gT` / `gt` | previous / next tab |
| `1`–`9` | jump to tab N |
| `/` | find a job in the current tab (ignores case); `enter` selects it |
| `j` / `k`, `gg` / `G` | move between checks |
| `enter` | open the selected check's job page |
| `o` | open the PR page |
| `y` | copy the PR's URL |
| `Y` | copy the selected job's URL |
| `n` | toggle notifications for the current PR |
| `b` | toggle notifications for the selected job |
| `N` | notification settings: event types and global mute |
| `a` | add a PR |
| `d` | unwatch the current PR (in every client) |
| `r` | ask the daemon to poll now |
| `R` | re-run failed CI: comment `/retest` for Prow, re-run failed Actions jobs |
| `?` | all keys (`?` or `esc` closes it) |
| `q` | quit this TUI (the daemon keeps running) |

## Merge queue

A PR waiting in GitHub's merge queue shows `Q` on its tab. Its header
starts with the queue state, its place in line, how long it has waited
and GitHub's estimate of the time left, e.g. `in merge queue, checks
running, 2nd in line, 12m ago, ~8m left`. The queue is also the first row of
the list, under a "Merge queue" header: select it and press `enter` to
open the queue page on GitHub. That row is not a check: it does not count
in "done", and has no bell of its own (`n` alerts cover the merge).
