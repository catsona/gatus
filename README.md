# gstatus

A terminal monitor for your GitHub pull requests, powered by `gh`.


## Build

Requires Go 1.26 or later.

```sh
go test -race ./...
sh scripts/build.sh
```

The build produces archives and checksums in `dist/` for macOS arm64, Linux arm64 and Linux amd64. Forgejo Actions runs tests and uploads the archives on each push and pull request. The workflow uses the `docker` runner label.
