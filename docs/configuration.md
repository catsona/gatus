# Configuration reference

The config path is `~/.gstatus/config.yaml`. The application creates defaults on its first interactive start. Unknown fields and invalid values reject the entire update. A running application keeps its last valid config and displays the error.

The settings screen saves valid changes immediately. If an external edit conflicts with an open editor, `l` discards the draft and `w` saves the edited field. Other externally updated fields remain intact.

## Fields

Omitted fields use defaults. An explicit empty list disables a list of events or hotkeys. An empty repository list includes all accessible repositories.

```yaml
version: 1
repositories: []
refresh:
  enabled: true
  interval: 30s
view:
  filter: open
  sort: updated
  descending: true
  search: ""
read:
  mark_on_open: true
notifications:
  enabled: false
  webhook_url: ""
  events:
    - comment.created
    - thread.reply
    - review.submitted
    - ci.success
    - ci.failure
    - pr.state_changed
    - pr.created
  template: '{{.PR.Url}} {{.PR.Title}} status update: {{.Event.Summary}}'
```

`repositories` contains `owner/repo` names. The author is always the active GitHub user. The current implementation supports `github.com`.

`refresh.interval` accepts Go duration strings, with a minimum of `5s`. Account detection runs independently every two seconds, including when auto-refresh is off.

`view.filter` accepts `all`, `open`, `closed`, `draft` and `merged`. `open` includes drafts. `closed` excludes merged PRs. Historical PRs load when the selected filter needs them.

`view.sort` accepts `created`, `updated`, `ci`, `review` and `message`. Message time includes issue comments and review-thread comments. Unknown times sort last. Search matches repository, PR number and title locally.

`read.mark_on_open` acknowledges displayed events after the browser command succeeds. Events arriving during that command stay unread. Copying never acknowledges events.

## Webhook events

The webhook receives a POST with `Content-Type: application/json` and one field:

```json
{"text":"https://github.com/owner/repo/pull/42 Fix build status update: CI passed"}
```

| Event | Trigger |
| --- | --- |
| `comment.created` | Another author posts a PR comment or review-thread comment |
| `thread.reply` | Another author comments in a review thread you already participated in |
| `review.submitted` | Another author submits a review |
| `ci.success` | All observed current CI completes successfully |
| `ci.failure` | All observed current CI completes, with at least one failure |
| `pr.state_changed` | The PR changes state or draft status |
| `pr.created` | A new authored PR appears after the initial baseline |

Bot activity counts. Your own comments and reviews do not trigger notifications. A reply emits `thread.reply` without a duplicate `comment.created` event.

CI uses the current head and merge revisions, workflow runs and attempts. A terminal result must remain unchanged across two observations at least five seconds apart. With a `30s` interval, this usually takes the next refresh. Superseded cancellations do not notify. External checks display status, but reruns apply only to GitHub Actions.

GitHub does not expose a final list of every workflow that might start later. A delayed workflow can therefore produce a later CI result. Missing permissions, incomplete pagination and rate limits display errors instead of a successful CI result.

Notifications start after the account baseline. Enabling them does not replay history. The persistent queue sends only for the active account. Changing the URL or template affects the next delivery. Disabling events removes their pending deliveries, including paused accounts.

HTTP 2xx completes delivery. Transport errors, HTTP 408, 429 and 5xx allow five total attempts, with delays of 2, 5, 15 and 60 seconds. `Retry-After` can extend the delay. Each request has a ten-second timeout. Other HTTP errors remain failed in the database. Delivery can repeat if a receiver accepts a request but its response is lost.

## Template fields

Templates use Go `text/template`. The root contains `.PR`, `.Account` and `.Event`.

| Object | Fields |
| --- | --- |
| `.PR` | `Url`, `Title`, `Repository`, `Number`, `State`, `Draft`, `HeadSHA`, `MergeSHA`, `CIStatus`, `ReviewStatus` |
| `.Account` | `Login`, `ID`, `Host` |
| `.Event` | `Type`, `Summary`, `Url`, `Actor`, `OccurredAt`, `Field`, `Old`, `New` |

The remaining PR timestamp, check and workflow fields are defined in [`internal/model/model.go`](../internal/model/model.go). The JSON encoder escapes the rendered text.

```yaml
notifications:
  enabled: true
  webhook_url: https://example.org/hook
  events: [ci.failure, review.submitted, thread.reply]
  template: '{{.PR.Url}} {{.PR.Title}}: {{.Event.Summary}} ({{.Event.Type}})'
```

## Hotkeys

The `keys` map overrides individual actions. Key names accept characters, arrows, `enter`, `tab`, `esc`, `space`, navigation keys and `f1` through `f63`. Modifiers are `ctrl+`, `alt+`, `shift+` and `super+`. Equivalent keys cannot control different actions.

```yaml
keys:
  up: [up, k]
  down: [down, j]
  focus_next: [tab]
  focus_previous: [shift+tab]
  open: [enter, o]
  copy_url: [y]
  mark_read: [m]
  mark_unread: [u]
  refresh: [r]
  toggle_auto_refresh: [a]
  refresh_interval: [i]
  search: [/]
  filter: [f]
  sort: [s]
  rerun_all: [ctrl+r]
  rerun_failed: [R]
  settings: [","]
  help: ["?"]
  back: [esc]
  quit: [q, ctrl+c]
```

Editor controls remain Enter to save and Esc to cancel. The multiline template editor uses Ctrl+S to save. Ctrl+C always quits.

## State and desktop commands

`~/.gstatus/state.db` contains account snapshots, read marks and the delivery queue. The account key combines the GitHub host with its immutable user ID. Credentials stay in memory. The application uses private directory and file permissions. Only one process can open the database at a time.

Browser launch uses `GH_BROWSER`, then `BROWSER`, then `open` on macOS or `xdg-open` on Linux. Configured commands accept quoted arguments and an optional `%s` URL placeholder.

Copy uses `pbcopy` on macOS and `wl-copy`, `xclip` or `xsel` on Linux. If no helper exists, it requests clipboard access through the terminal's OSC 52 support.
