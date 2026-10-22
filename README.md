# gstatus

A terminal monitor for your GitHub pull requests, powered by `gh`. Keep CI, reviews, comments and replies visible in one terminal.

## Install and start

Install Go 1.26+ and [GitHub CLI](https://cli.github.com/) 2.96+, then run:

```sh
go install git.pegasko.art/catsona/gstatus/cmd/gstatus@latest
gh auth login
gstatus
```

For a private repository, Git needs access to `git.pegasko.art`. Set `GOPRIVATE=git.pegasko.art/catsona/gstatus` for the install command if needed.

Versioned archives for macOS ARM64, Linux ARM64 and Linux AMD64 are available in [Forgejo Releases](https://git.pegasko.art/catsona/gstatus/releases). Run `gstatus --version` to see the installed version.

The top bar controls the list: search, state filters, sorting, auto-refresh, interval and refresh all. Settings, help and quit are also at the top.

The bottom bar acts on the selected PR: open, copy, mark read or unread, refresh, rerun failed or all CI jobs, and cancel CI. Both bars support mouse clicks and keyboard focus with Tab, Shift+Tab or left/right arrows. Press Enter to activate the focused button. Up/down arrows or `j`/`k` select a PR. Double-click a PR to open it.

The bars wrap with the terminal width. In short terminals, buttons show their shortcuts, and the status line names the focused action. Press `?` for all shortcuts and `,` for settings.

The indicator before each PR shows `…` while queued, an animated spinner while refreshing, and `·` while idle. It tracks full-list and selected-PR refreshes separately from unread marks.

The Branch column shows merge conflicts (`conflict`), no conflicts (`ok`), or a pending result (`unknown`). Narrow terminals use `!!`, `ok` and `?`.

The state filter limits which PRs are monitored and refreshed. Text search only hides rows. Read marks survive state filter changes.

| Key | Action |
| --- | --- |
| Enter / `o` | Open in the browser |
| `y` | Copy the PR URL |
| `m` / `u` | Mark read / unread |
| `r` / `a` / `i` | Refresh / toggle auto-refresh / change interval |
| `p` | Refresh only the selected PR |
| `/` / `f` / `s` | Search / filter / sort |
| `S` | Switch sort direction between ASC and DESC |
| `R` / Ctrl+R | Rerun failed jobs / all jobs |
| `x` | Cancel active CI for the selected PR |
| `q` | Quit |

All settings and state live in `~/.gstatus/`. Edit `config.yaml` or use the settings screen. Changes apply without a restart. The first load marks existing PRs read. Copying never changes read state.

The header shows the active `gh` login. Switching with `gh auth switch` changes the displayed account within the next account check. Each account keeps separate read state.

- [Configuration reference](docs/configuration.md): repositories, hotkeys, webhook events and templates.
- [Development and binary builds](docs/development.md): tests, packages and Forgejo artifacts.

