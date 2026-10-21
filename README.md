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

The main screen includes search, state filters, sorting and refresh controls. Use arrows or `j`/`k` to select a PR. Click controls or double-click a PR to open it. Press `?` for shortcuts and `,` for settings.

| Key | Action |
| --- | --- |
| Enter / `o` | Open in the browser |
| `y` | Copy the PR URL |
| `m` / `u` | Mark read / unread |
| `r` / `a` / `i` | Refresh / toggle auto-refresh / change interval |
| `p` | Refresh only the selected PR |
| `/` / `f` / `s` | Search / filter / sort |
| `R` / Ctrl+R | Rerun failed jobs / all jobs |
| `x` | Cancel active CI for the selected PR |
| `q` | Quit |

All settings and state live in `~/.gstatus/`. Edit `config.yaml` or use the settings screen. Changes apply without a restart. The first load marks existing PRs read. Copying never changes read state.

The header shows the active `gh` login. Switching with `gh auth switch` changes the displayed account within the next account check. Each account keeps separate read state.

- [Configuration reference](docs/configuration.md): repositories, hotkeys, webhook events and templates.
- [Development and binary builds](docs/development.md): tests, packages and Forgejo artifacts.

