# Development

## Requirements

Install Go 1.26 or newer, Git and a POSIX shell. Interactive use also needs GitHub CLI 2.96 or newer and a terminal at least 40 columns wide and 10 rows high.

## Verify changes

Run the tests and race detector:

```sh
go test -race ./...
```

Run static checks and inspect formatting:

```sh
go vet ./...
gofmt -l cmd internal
```

Tests use fixture executables for GitHub CLI, browser and clipboard operations. Webhook tests use local HTTP servers. Tests do not access a GitHub account or rerun real CI.

## Build binaries

Run the build script from the repository root:

```sh
sh scripts/build.sh
```

The script writes these archives and `SHA256SUMS` to `dist/`:

- `gatus-<version>-darwin-arm64.tar.gz`
- `gatus-<version>-linux-arm64.tar.gz`
- `gatus-<version>-linux-amd64.tar.gz`

Each archive contains `gatus`. Builds use `CGO_ENABLED=0`. Set `GATUS_VERSION` to override the embedded version.

The GitHub Actions workflow runs on branch pushes, pull requests and manual dispatches. It checks formatting, runs vet and race tests, builds all targets and uploads `gatus-binaries`. Version tags such as `v0.1.0` publish separate GitHub Releases with the archives and `SHA256SUMS`.

`gatus --version` reports the linked build version. A version installed with `go install ...@v0.1.0` uses Go module metadata when linker flags are absent.

## Package boundaries

| Package | Responsibility |
| --- | --- |
| `cmd/gatus` | Start and stop the application |
| `internal/config` | Parse, validate, write and reload YAML configuration |
| `internal/github` | Run GitHub CLI requests, authenticate and normalize responses |
| `internal/monitor` | Coordinate polling, account generations, events and aggregate CI |
| `internal/state` | Store account namespaces and notification queues in bbolt |
| `internal/notify` | Render templates and deliver webhooks with bounded retries |
| `internal/tui` | Render the interface and handle keyboard or mouse actions |
| `internal/desktop` | Open browser links and copy URLs |
| `internal/app` | Manage startup and worker lifetimes |

Keep network operations outside the TUI update loop. Bind data and mutation requests to captured credentials. Commit observed state and new deliveries in one transaction. Preserve event sequence boundaries when adding acknowledgement actions.
