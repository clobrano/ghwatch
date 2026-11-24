# The ghwatch TUI

Run `ghwatch` to open it. Its colors are a
[Catppuccin](https://catppuccin.com) flavor: Frappé by default, or the one
set by `theme` in config.toml (`latte`, `frappe`, `macchiato` or `mocha`).
ghwatch keeps the terminal's background, so pick the flavor that matches
it. From top to bottom:

- **Title bar:** the app name, then in words how many PRs are in each
  state (`4 PRs: 1 failing · 1 running · 1 passed · 1 merged`), then, when
  there are some, how many changed since you last looked and how many
  new comments there are in all. It says `muted` while notifications are
  muted, and when GitHub was last polled; the poll time turns yellow
  ("last poll … ago") while polling fails. On a narrow screen the
  tagline gives way first.
- **Tabs:** one per PR, in the order they were added. A label is the
  tab's number (its `1`–`9` key), the PR's state icon, the number and a
  short name, and a bell when alerts are on. A PR from a repository other
  than the most common one gets a short repository prefix (`osac#58`).
  The active tab is highlighted. An orange `•` marks a PR that changed
  since you last looked at it: its state changed (say, running to failed),
  it got a new push, or it was merged or closed. Pressing any key while
  on its tab clears the mark, in every open TUI. A comment mark and a
  count show the PR's new comments; they clear the same way.
- **PR header:** the first line is like the top of GitHub's PR page: the
  state chip (`OPEN` green, `MERGED` mauve, `CLOSED` red), the title, the
  reference (`org/repo#123`), and the author at the right; a long title
  is cut before the reference. Labelled rows follow, each only when it
  has something to say:
  - `CI:` the head's checks: progress, failures (saying how many are
    required: `2 failing (1 required)`, `1 failing, none required`),
    jobs still running, and the commit they run on with its push time.
    While the PR is in a merge queue, the queue comes first.
  - `comments:` new comments and unresolved review threads.
  - `alerts:` the notifications set on the PR, its jobs and threads.
  - `added:` why ghwatch added the PR on its own (`your review is
    requested`, `assigned to you`); see `[autoadd]` in the README.
  - `labels:` each label in bold in its GitHub color, lightened just
    enough to read on the background when GitHub's color is too dark
    (darkened with the light flavor).

  On a short screen the last rows give way, so the list keeps its room.
- **List:** each PR has two lists, its comments and its jobs, and `v`
  switches between them. A row of tabs above the list names both,
  comments first, with the one shown highlighted; each tab carries its
  own count: new comments, and failing jobs (or running ones while none
  has failed), e.g. `Comments 3 new   Checks 2 failing`. The list sits
  in a box that ends after its last row. ghwatch opens on the comments.
- **Job list:** a Check / Time / Source header and the selected job
  highlighted. Jobs are split into groups, each under a
  header with its count: **Failed**, then **Running** (including
  pending), then **Passed** (including skipped), then **Cancelled** at
  the bottom. Within a group, jobs with a bell (`b`) come first; the
  others stay in the order they were first seen. Ringing or silencing a
  bell does not move the job: the order catches up the next time you
  open the tab. When a job changes group, the selection follows it.
  A job's name is followed by its bell, if any, and by `opt` when
  branch protection does not require it; when a PR has required checks,
  a failing optional one does not turn the PR red. Only the state icon
  is colored: times and sources stay neutral, and long source names are
  shortened (`GitHub Advanced Security` is `GH Security`).
- **Comment list:** a Thread / Author / Last header. One row is the
  **conversation**: the PR's comments and review summaries (approvals,
  change requests). Every other row is a **review thread** on code,
  named by file and line (`prd.md:42`), with as many parent directories
  as it takes to tell apart files of the same name; an expanded thread
  starts with the full path. A row shows its comments (`· 3`), how many
  are new (`· 1 new`), an `outdated` tag when the code changed since,
  the start of the latest comment, and who wrote it and how long ago. Groups, top to bottom: **New**
  (threads with new comments, marked `•`), **Open**, and **Resolved**.
  Within a group, threads with a bell come first, then the conversation,
  then the most recently commented. `e` expands the selected thread:
  each of its comments shows below it in full, oldest first, with its
  author, age and review state, and new ones marked `•`. While a thread
  is expanded, `j`/`k` step through its comments, and `enter`, `Y` open
  or copy the selected comment; folded, they move from thread to thread.
  `e` again folds it.
- **Footer:** the main keys of the list shown (`h/l PR · j/k job · enter
  open · b bell · v comments · ? help`; in the comments list also
  `e expand · r reply`), and the connection to the daemon. On narrow
  terminals the less important hints give way, and `? help` always
  stays; every other key is in the `?` panel. Messages replace the hints
  for a few seconds.

`?` opens the full list of keys in a panel, grouped by topic; `tab`
switches it to the legend, which shows every state icon and mark and
what it means (a tab's icon is the state of the PR's required checks,
or of all checks when none is required). `N`
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
| `e` | expand the selected comment thread to all its comments, or fold it |
| `J` / `K` | (comments) scroll the selected comment when it is taller than the list |
| `r` | (comments) reply in the selected thread; on the conversation, comment on the PR |
| `R` | (comments) resolve or unresolve the selected thread; (checks) re-run failed CI |
| `/` | find a job, or a thread by file, author or text (ignores case); `enter` selects it |
| `j` / `k`, `gg` / `G` | move between jobs, threads, or the comments of an expanded thread |
| `enter` | open the selected job's page, or the selected comment (a thread's latest) |
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
| `p` | ask the daemon to poll now |
| `?` | all keys; `tab` there shows the legend of icons and marks (`?` or `esc` closes it) |
| `q` | quit this TUI (the daemon keeps running) |

## Auto-added PRs

With `[autoadd]` on (see the README), ghwatch adds the PRs requesting
your review or assigned to you. Their tab gets the `•` mark when they
arrive, and the header an `added:` row saying why. They stay until you
remove them with `d`, which also keeps them from being added again; the
README's "Keeping the tab list short" covers the other ways.

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

## Replying and resolving

`r` on a thread opens your editor (`$VISUAL`, else `$EDITOR`, else `vi`)
on an empty file, like `git commit`. Write the reply in markdown, save and
quit; ghwatch asks before posting it (`Reply on pkg/foo.go:42 (3
lines)?`). An empty file posts nothing. On the conversation row the reply
is a new comment on the PR; on a code thread it is a reply in that
thread, like GitHub's Reply button. If you decline, or posting fails, the
text is kept: `r` again reopens it. The reply shows in the list after the
poll that follows, and, being yours, it is never new.

`R` on a code thread resolves it, or unresolves a resolved one, after a
confirmation. The conversation cannot be resolved. In the checks list,
`R` re-runs failed CI: it comments `/retest` for Prow and re-runs failed
Actions jobs.

Both post as your `gh` login (or `$GITHUB_TOKEN`), through the daemon.

ghwatch fetches the last 20 overview comments and reviews, and the last
50 review threads with their last 5 comments each, keeping up to 4,000
characters of each comment. Counts of new comments are within those, and
an expanded thread says how many earlier comments are only on GitHub:
`enter` opens the thread there. A comment taller than the list is cut
with a `↓ 14 more lines · J` line: `J` and `K` scroll it a line at a
time, `j` and `k` still jump to the next or previous comment.

## Merge queue

A PR waiting in GitHub's merge queue shows `Q` on its tab. Its header
starts with the queue state, its place in line, how long it has waited
and GitHub's estimate of the time left, e.g. `in merge queue, checks
running, 2nd in line, 12m ago, ~8m left`. The queue is also the first row of
the list, under a "Merge queue" header: select it and press `enter` to
open the queue page on GitHub. That row is not a check: it does not count
in "done", and has no bell of its own (`n` alerts cover the merge).
