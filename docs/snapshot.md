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
      "ci_restarted": true, "item_finished": true
    }
  },
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
      "enqueued_at": "…",
      "eta_seconds": 480       // GitHub's estimate, when it gives one
    },
    "alerts": true,            // notifications on for this item (default false)
    "watched_checks": ["e2e"], // jobs with their own notifications (omitted when none)
    "error": "",               // last fetch of this item failed
    "updated_at": "…",
    "checks": [{               // stable order: first seen, first listed
      "name": "e2e-aws-ovn",
      "source": "Prow",        // Actions | Prow | Status | app name
      "state": "failed",       // pending | running | passed | failed | skipped | cancelled
      "required": true,        // required by branch protection
      "started_at": "…",       // optional
      "completed_at": "…",     // optional
      "url": "https://prow…"   // job page
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
| `retest` | `id` | re-trigger failed CI |
