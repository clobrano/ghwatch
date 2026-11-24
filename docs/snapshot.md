# State snapshot (schema 1)

The daemon writes its whole state to `$XDG_STATE_HOME/ghwatch/snapshot.json`
after every change, and sends the same object to socket clients. External
tools may read it. New fields can be added without notice. Removing or
changing the meaning of a field bumps `schema`.

```jsonc
{
  "schema": 1,
  "generated_at": "2026-09-30T10:42:00Z", // last change of any kind
  "polled_at": "2026-09-30T10:42:00Z",    // last successful poll (omitted before the first)
  "stale": false,              // last poll failed; items are the last known state
  "error": "",                 // why, when stale
  "settings": {
    "mute": false,             // global mute; per-item alerts are kept
    "events": {                // event types that notify, for items with alerts on
      "check_failed": true, "all_passed": true, "check_started": true,
      "ci_restarted": true, "item_finished": true, "comment": true
    }
  },
  "dismissed": ["pr:org/repo#7"], // auto-added items you removed: not added again (omitted when none)
  "notice": "removed org/repo#7: no longer requests your review", // what the daemon did on its own (omitted when none)
  "notice_at": "…",           // when
  "items": [{                  // watchlist order
    "kind": "pr",
    "id": "pr:org/repo#123",   // kind-namespaced, lower case
    "repo": "org/repo",
    "number": 123,
    "title": "Fix lease renewal race",
    "author": "clobrano",
    "url": "https://github.com/org/repo/pull/123",
    "branch": "lease-race",
    "head_sha": "a1b2c3d…",    // empty until the first successful fetch
    "pushed_at": "…",          // commit date of the head
    "lifecycle": "open",       // open | merged | closed
    "state": "failed",         // aggregate; queued while in a merge queue; merged/closed once finished
    "merge_queue": {           // only while the PR waits in a merge queue
      "state": "awaiting_checks", // queued | awaiting_checks | mergeable | unmergeable | locked
      "position": 2,           // 1 = next to merge
      "url": "https://github.com/org/repo/queue/main", // the queue page
      "enqueued_at": "…",
      "eta_seconds": 480       // GitHub's estimate, when it gives one
    },
    "alerts": true,            // notifications on for this item (default false)
    "unseen": true,            // changed since a user last looked at it (omitted when false)
    "labels": [{"name": "lgtm", "color": "0e8a16"}], // GitHub labels (omitted when none)
    "watched_checks": ["e2e"], // jobs with their own notifications (omitted when none)
    "watched_threads": ["conversation"], // comment threads with their own notifications, by id (omitted when none)
    "added": ["review requested", "assigned"], // why ghwatch added the item on its own (omitted when added by hand)
    "review": {                // your part in the review (omitted when none)
      "requested": true,       // your review is requested now
      "team": "nhc-reviewers", // ...from this team, not from you directly (omitted then)
      "state": "approved",     // your latest review: approved | changes_requested | commented | dismissed | pending (a draft)
      "at": "…"                // when you submitted it
    },
    "muted": true,             // alerts, watched_checks and watched_threads silenced, kept to restore (omitted when false)
    "comments_read_at": "…",   // comments newer than this are new; set at the first fetch (omitted before)
    "threads": [{              // the conversation, then review threads in GitHub's order
      "id": "conversation",    // "conversation", or GitHub's review thread ID
      "path": "pkg/foo.go",    // review threads only
      "line": 42,              // review threads only, when known
      "resolved": false,       // omitted when false
      "outdated": false,       // omitted when false
      "total": 12,             // comments in the thread
      "url": "https://…",
      "comments": [{           // the latest ones, oldest first
        "id": "IC_…",
        "author": "alice",
        "body": "Shouldn't this…", // the text, as plain text, at most 4000 characters
        "created_at": "…",
        "url": "https://…",
        "review": "approved",  // review summaries: approved | changes_requested | commented | dismissed
        "mine": true,          // written by the user ghwatch runs as (omitted when false)
        "ignored": true        // author in ignore_authors (omitted when false)
      }]
    }],
    "error": "",               // last fetch of this item failed
    "updated_at": "…",
    "checks": [{               // stable order: first seen, first listed
      "name": "e2e-aws-ovn",
      "source": "Prow",        // Actions | Prow | Status | app name
      "state": "failed",       // pending | running | passed | failed | skipped | cancelled
      "required": true,        // required by branch protection
      "started_at": "…",       // optional
      "completed_at": "…",     // optional
      "url": "https://…",      // as on GitHub's PR page: Actions job page (?pr=N), GitHub check page for other apps' check runs, target URL for a status
      "details_url": "https://…" // the app's own page (e.g. a Konflux pipeline run), when it differs
    }]
  }]
}
```

## Socket protocol

`$XDG_RUNTIME_DIR/ghwatch/ghwatch.sock` carries newline-delimited JSON.

- Server to client: `{"type":"snapshot","snapshot":{…}}` on connect and
  after every change. `{"type":"result","seq":N,"info":"…","error":"…"}`
  answers command `N`.
- Client to server: `{"type":"command","seq":N,"command":{"op":"…",…}}`
  with these `op` values:

| op | fields | effect |
| --- | --- | --- |
| `add` | `id`: URL or `owner/repo#N` | validate the PR exists and watch it |
| `unwatch` | `id` | stop watching |
| `poll` | | poll now |
| `alerts` | `id`, `on` | per-item notifications |
| `check_alerts` | `id`, `check`, `on` | notifications for one job, by name |
| `events` | `events`: `{type: bool}` | enable or disable event types |
| `mute` | `on` | global mute |
| `seen` | `id` | clear the item's unseen mark |
| `mute_item` | `id`, `on` | mute the item's notifications, keeping them, or restore them |
| `clear_alerts` | `id` | turn off the item's alerts, job and thread bells, and unmute it |
| `thread_alerts` | `id`, `thread`, `on` | notifications for one comment thread, by id |
| `comments_read` | `id` | mark the item's comments read, up to its latest |
| `reply` | `id`, `thread`, `body` | post `body` in a thread: a PR comment for `conversation`, else a reply in the review thread |
| `resolve` | `id`, `thread`, `on` | resolve (`on` true) or unresolve a review thread |
| `retest` | `id` | re-trigger failed CI |
