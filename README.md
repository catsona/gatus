# gstatus

`gstatus` monitors your GitHub pull requests in a terminal. It shows CI, review, comment and reply activity, and lets you open, copy, refresh, rerun or cancel CI for the selected pull request.

## Requirements

- Go 1.26 or newer.
- GitHub CLI 2.96 or newer.
- A terminal at least 40 columns wide and 10 rows high for the full layout.

The application uses the active `gh` account and supports GitHub.com. For a private module checkout, set `GOPRIVATE=git.pegasko.art/catsona/gstatus`.

## Install

```sh
go install git.pegasko.art/catsona/gstatus/cmd/gstatus@latest
gh auth login
gstatus
```

Versioned archives for macOS ARM64, Linux ARM64 and Linux AMD64 are available from [Forgejo Releases](https://git.pegasko.art/catsona/gstatus/releases). Check an installed binary with `gstatus --version`.

## Use the monitor

The top bar controls search, the state filter, sorting, sort direction, auto-refresh, the refresh interval and full refresh. Settings, help and quit are also available there.

The bottom bar acts on the selected pull request. It opens or copies the URL, changes the read mark, refreshes the pull request, reruns failed or all CI jobs, and cancels active CI. Use Tab, Shift+Tab or the arrow keys to move focus, then press Enter. Use the mouse on either bar. Use the arrow keys or `j` and `k` to select a pull request. Double-click a row to open it.

The bars wrap to fit the terminal. In a short terminal, controls show their shortcuts and the status line names the focused action. Press `?` for the complete shortcut list and `,` for settings.

The indicator before each pull request shows `…` for queued work, an animated spinner while work runs and `·` while idle. It tracks full-list and selected-pull-request refreshes separately from read marks.

The Branch column shows `conflict`, `ok` or `unknown`. Narrow terminals show `!!`, `ok` or `?`. The state filter controls which pull requests are monitored and refreshed. Text search only hides rows. Read marks survive state filter changes.

| Key | Action |
| --- | --- |
| Enter / `o` | Open the pull request in a browser |
| `y` | Copy the pull request URL |
| `m` / `u` | Mark read / unread |
| `r` / `a` / `i` | Refresh / toggle auto-refresh / change interval |
| `p` | Refresh only the selected pull request |
| `/` / `f` / `s` | Search / filter / sort |
| `S` | Switch between ascending and descending sort |
| `R` / Ctrl+R | Rerun failed jobs / all jobs |
| `x` | Cancel active CI for the selected pull request |
| `q` | Quit |

## Configuration and state

Configuration and state are stored in `~/.gstatus/`. Edit `config.yaml` or use the settings screen. Valid changes apply without a restart. The first load marks existing pull requests as read. Copying a URL never changes its read state.

The header shows the active `gh` login. After `gh auth switch`, the application updates the account during its next account check. Each account has separate read state.

- [Configuration reference](docs/configuration.md)
- [Development and binary builds](docs/development.md)
