# The ghwatch TUI

Run `ghwatch` to open it. The screen follows the style of
[jira-tabbed-tui](https://github.com/clobrano/jira-tabbed-tui), from top to
bottom:

- **Title bar:** the app name and how many PRs are watched and in which
  state (failed, running, passed, queued, merged or closed). It also shows
  how many PRs have alerts, how many changed since you last looked
  (`•2 new`), how many new comments there are in all, whether
  notifications are muted, and when
  GitHub was last polled. The poll time turns yellow ("last poll … ago")
  while polling fails.
- **Tabs:** one per PR, in the order they were added. A label is the
  tab's number (its `1`–`9` key), the PR's state icon, the number and a
  short name, and a bell when alerts are on. A PR from a repository other
  than the most common one gets a short repository prefix (`osac#58`).
  The active tab is highlighted. An orange `•` marks a PR that changed
  since you last looked at it: its state changed (say, running to failed),
  it got a new push, or it was merged or closed. Pressing any key while
  on its tab clears the mark, in every open TUI. A comment mark and a
  count show the PR's new comments; they clear the same way.
- **PR header:** reference, title and author, then head commit, push
  time, progress, failures and unresolved review threads. The PR's
  labels show below as chips in their GitHub colors.
- **List:** each PR has two lists, its comments and its jobs, and `v`
  switches between them. The box's top border names both, comments
  first, with the one shown highlighted and the number of new comments
  (`Comments 3 new`). A PR opens on its jobs.
- **Job list:** a Check / Time / Source header and the selected job
  highlighted. Jobs are split into groups, each under a
  header with its count: **Failed**, then **Running** (including
  pending), then **Passed** (including skipped), then **Cancelled** at
  the bottom. Within a group, jobs with a bell (`b`) come first; the
  others stay in the order they were first seen. Ringing or silencing a
  bell does not move the job: the order catches up the next time you
  open the tab. When a job changes group, the selection follows it.
  Jobs marked `opt` are not required by branch protection. When a PR
  has required checks, a failing optional one does not turn the PR red.
- **Comment list:** a Thread / Last / Author header. One row is the
  **conversation**: the PR's comments and review summaries (approvals,
  change requests). Every other row is a **review thread** on code,
  named by file and line (`pkg/foo.go:42`). A row shows its number of
  comments, or how many are new, the start of the latest comment, how
  long ago and by whom it was written. Groups, top to bottom: **New**
  (threads with new comments, marked `•`), **Open**, and **Resolved**.
  Within a group, threads with a bell come first, then the conversation,
  then the most recently commented.
- **Footer:** what the main keys do (`h/l prev/next PR · j/k next/prev job
  · v comments · enter open job · …`), for the list shown, and the
  connection to the daemon.
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
| `v` | switch between the checks and the comments |
| `/` | find a job, or a thread by file, author or text (ignores case); `enter` selects it |
| `j` / `k`, `gg` / `G` | move between jobs or threads |
| `enter` | open the selected job's page, or the thread's latest comment |
| `o` | open the PR page |
| `y` | copy the PR's URL |
| `Y` | copy the selected job's or comment's URL |
| `n` | toggle notifications for the current PR |
| `b` | toggle notifications for the selected job or thread |
| `m` | mute all of this PR's notifications, or restore them as they were |
| `c` | clear all of this PR's notifications, after a confirmation |
| `N` | notification settings: event types and global mute |
| `a` | add a PR |
| `d` | unwatch the current PR (in every client) |
| `r` | ask the daemon to poll now |
| `R` | re-run failed CI: comment `/retest` for Prow, re-run failed Actions jobs |
| `?` | all keys (`?` or `esc` closes it) |
| `q` | quit this TUI (the daemon keeps running) |

## Comments

A comment is new when it was written after you last looked at the PR:
pressing any key while on its tab marks its comments read, in every open
TUI. They stay marked as new until you leave the tab, so you can read
them. Your own comments are never new, and neither are those of authors
in `ignore_authors` (see the README's configuration). Comments that were
already there when you started watching a PR are not new.

`b` on a thread rings its bell: each new comment in it notifies, like a
job's bell. On the conversation row it covers every new overview comment
and review. With the PR's alerts on (`n`), every new comment notifies, as
long as "new comment" is enabled in `N`. Several comments found in one
poll make a single notification. `m` and `c` mute and clear thread bells
together with the rest.

ghwatch fetches the last 20 overview comments and reviews, and the last
50 review threads with their last 5 comments each. Counts of new
comments are within those.

## Merge queue

A PR waiting in GitHub's merge queue shows `Q` on its tab. Its header
starts with the queue state, its place in line, how long it has waited
and GitHub's estimate of the time left, e.g. `in merge queue, checks
running, 2nd in line, 12m ago, ~8m left`. The queue is also the first row of
the list, under a "Merge queue" header: select it and press `enter` to
open the queue page on GitHub. That row is not a check: it does not count
in "done", and has no bell of its own (`n` alerts cover the merge).
