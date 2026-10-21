# Build and verify gstatus

Prerequisites: Go 1.26+, Git, and a shell. Interactive use also needs `gh` 2.96+ and a terminal of at least 40 columns by 10 rows.

## Run checks

1. Run the tests and race detector:

   ```sh
   go test -race ./...
   ```

2. Run static checks and inspect formatting:

   ```sh
   go vet ./...
   gofmt -l cmd internal
   ```

Tests use fixture executables for `gh`, browser and clipboard operations. Webhook tests use local HTTP servers. Tests do not access your GitHub account or rerun real CI.

## Build binaries

Run the build script from the checkout:

```sh
sh scripts/build.sh
```

It produces these archives and `SHA256SUMS` in `dist/`:

- `gstatus-darwin-arm64.tar.gz`
- `gstatus-linux-arm64.tar.gz`
- `gstatus-linux-amd64.tar.gz`

Each archive contains `gstatus`. The builds use `CGO_ENABLED=0`. Set `GSTATUS_VERSION` to override the version embedded by the script.

The Forgejo workflow runs on pushes, pull requests and manual dispatch. It needs a runner with the `docker` label. It checks formatting, runs vet and race tests, builds all targets, and uploads `gstatus-binaries`. Download the artifact from the workflow run's page.

The workflow targets Forgejo 9 and uses `upload-artifact@v3`. A successful local build does not confirm a remote runner completed the workflow.

## Extend a component

The package boundaries are:

| Package | Contract |
| --- | --- |
| `internal/github` | Account-bound `gh` requests, pagination and Actions reruns |
| `internal/monitor` | Snapshots, account generations, event reconciliation and aggregate CI |
| `internal/state` | Account namespaces and transactional notification storage |
| `internal/notify` | Template rendering and bounded webhook delivery |
| `internal/config` | YAML validation, atomic saves and live reload |
| `internal/desktop` | Browser and clipboard commands |
| `internal/tui` | Rendering and keyboard/mouse actions |
| `internal/app` | Startup and worker lifetimes |

Keep network operations outside the TUI update loop. Bind data and mutation requests to captured credentials. Commit observed state and its new deliveries together. Preserve event sequence boundaries when adding acknowledgement actions.
