# Configuration reference

The application reads `~/.gstatus/config.yaml`. It creates the file with defaults on the first interactive start. Unknown fields and invalid values reject the complete update. The running application keeps the last valid configuration and displays the error.

The settings screen saves valid changes immediately. If an external edit changes a field while it is open, press `l` to load the new value or `w` to save the draft. Other externally changed fields remain intact.

## Fields

Omitted fields use their defaults. An empty repository list includes every accessible repository. An empty event or hotkey list disables that list.

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
herdr:
  enabled: false
  root: ~
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

`repositories` contains `owner/repo` names. The application shows pull requests authored by the active GitHub user. The current implementation supports `github.com`.

`herdr.enabled` enables the selected-PR Herdr action. When enabled, `herdr.root` is the working directory for newly created Herdr workspaces; `~` refers to your home directory. The action focuses an existing workspace for the PR or creates a new one when the previous workspace was closed. gstatus does not clone a repository or start an agent in the workspace.

`refresh.interval` accepts Go duration strings and must be at least `5s`. Account detection runs every two seconds, even when auto-refresh is disabled.

`view.filter` accepts `all`, `open`, `closed`, `draft` and `merged`. The filter controls discovery, monitoring and manual or automatic refreshes. `open` includes drafts. `closed` excludes merged pull requests. Text search remains local and only hides rows.

When a pull request leaves the selected filter, the application records its final state transition and stops polling it. A pull request that reopens is handled in the same way. Cached read marks remain available when you select that state again.

`view.sort` accepts `created`, `updated`, `ci`, `review` and `message`. Unknown timestamps sort last. Search matches the repository, pull request number and title.

`view.descending` selects descending order when `true` and ascending order when `false`. Use the direction control or press `S` to switch. The setting applies to every sort field and is saved immediately. If an existing configuration assigns `S` to another action, the direction control remains available and the default shortcut stays unbound.

Review status colors are green for approval, red for requested changes, yellow for comments and gray when no verdict is available. Pull request state colors are green for open, red for closed, purple for merged and gray for draft. The Branch column uses red for `conflict`, green for `ok` and the default color for `unknown`.

## Selected refresh and CI actions

`refresh_selected` updates only the selected pull request's CI, reviews and comments. It does not rediscover the list or fetch other pull requests, and it preserves read marks. `refresh` updates the full list.

`cancel_ci` cancels queued and running GitHub Actions workflows for the selected pull request's current revision. Completed workflows and external CI are skipped. The action checks the active account and revision again before sending requests.

The rerun actions apply to the latest completed GitHub Actions runs for the current revision. Rerun failed starts only failed jobs. External CI is displayed but cannot be rerun.

## Webhook events

The webhook receives a JSON POST with one `text` field:

```json
{"text":"https://github.com/owner/repo/pull/42 Fix build status update: CI passed"}
```

| Event | Trigger |
| --- | --- |
| `comment.created` | Another author posts a pull request or review-thread comment |
| `thread.reply` | Another author replies in a review thread you joined |
| `review.submitted` | Another author submits a review |
| `ci.success` | All observed current CI completes successfully |
| `ci.failure` | All observed current CI completes with at least one failure |
| `pr.state_changed` | The pull request changes state or draft status |
| `pr.created` | A new authored pull request appears after the initial baseline |

Bot activity counts. Your own comments and reviews do not trigger notifications. A thread reply emits `thread.reply` without a duplicate `comment.created` event.

CI uses the current head and merge revisions, workflow runs and attempts. A terminal result must remain unchanged across two observations at least five seconds apart. Superseded cancellations do not notify. A delayed workflow can produce a later result because GitHub does not expose every future run.

Notifications start after the account baseline. Enabling notifications does not replay history. Changing the URL or template affects the next delivery. Disabling an event removes its pending deliveries.

Delivery succeeds for any HTTP 2xx response. Transport errors, HTTP 408, 429 and 5xx responses allow five total attempts with delays of 2, 5, 15 and 60 seconds. `Retry-After` can extend a delay. Other HTTP errors remain failed in the database. A receiver can accept a request and still receive a retry if its response is lost.

## Template fields

Templates use Go `text/template`. The root contains `.PR`, `.Account` and `.Event`.

| Object | Fields |
| --- | --- |
| `.PR` | `Url`, `Title`, `Repository`, `Number`, `State`, `Draft`, `HeadSHA`, `MergeSHA`, `CIStatus`, `ReviewStatus`, `BranchStatus` |
| `.Account` | `Login`, `ID`, `Host` |
| `.Event` | `Type`, `Summary`, `Url`, `Actor`, `OccurredAt`, `Field`, `Old`, `New` |

The remaining pull request, check and workflow fields are defined in [`internal/model/model.go`](../internal/model/model.go). The JSON encoder escapes the rendered text.

```yaml
notifications:
  enabled: true
  webhook_url: https://example.org/hook
  events: [ci.failure, review.submitted, thread.reply]
  template: '{{.PR.Url}} {{.PR.Title}}: {{.Event.Summary}} ({{.Event.Type}})'
```

## Hotkeys

The `keys` map overrides individual actions. Key names accept characters, arrows, `enter`, `tab`, `esc`, `space`, navigation keys and `f1` through `f63`. Modifiers are `ctrl+`, `alt+`, `shift+` and `super+`.

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
  refresh_selected: [p]
  toggle_auto_refresh: [a]
  refresh_interval: [i]
  search: [/]
  filter: [f]
  sort: [s]
  sort_direction: [S]
  open_herdr: [e]
  create_herdr: [shift+e]
  rerun_all: [ctrl+r]
  rerun_failed: [R]
  cancel_ci: [x]
  settings: [","]
  help: ["?"]
  back: [esc]
  quit: [q, ctrl+c]
```

Equivalent keys cannot control different actions. Editor controls remain Enter to save and Esc to cancel. The multiline template editor uses Ctrl+S to save. Ctrl+C always quits.

`open_herdr` lists live Herdr workspaces for the selected pull request. Choose a workspace to attach it to the PR and focus it. The current link is preselected when it is live. `create_herdr` creates a new workspace with `herdr.root` as its working directory, stores its workspace ID for the PR, and focuses it. Links are stored in `state.db`, and several PRs can reference one workspace. A workspace label or working directory does not identify a link. Repository cloning and agent startup remain manual.

## State and desktop commands

`~/.gstatus/state.db` contains account snapshots, read marks and the delivery queue. The account key combines the GitHub host with the immutable user ID. Credentials stay in memory. The application uses private directory and file permissions, and only one process can open the database at a time.

Browser launch uses `GH_BROWSER`, then `BROWSER`, then `open` on macOS or `xdg-open` on Linux. Copy uses `pbcopy` on macOS and `wl-copy`, `xclip` or `xsel` on Linux. If no clipboard helper exists, the application requests clipboard access through terminal OSC 52 support.
