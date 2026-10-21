#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

gstatus_version=${GSTATUS_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || printf dev)}
mkdir -p dist

for gstatus_target in darwin/arm64 linux/arm64 linux/amd64; do
    gstatus_os=${gstatus_target%/*}
    gstatus_arch=${gstatus_target#*/}
    gstatus_name="gstatus-${gstatus_version}-${gstatus_os}-${gstatus_arch}"

    mkdir -p "dist/$gstatus_name"
    CGO_ENABLED=0 GOOS="$gstatus_os" GOARCH="$gstatus_arch" go build -trimpath -ldflags "-s -w -X main.version=$gstatus_version" -o "dist/$gstatus_name/gstatus" ./cmd/gstatus
    LC_ALL=C tar -czf "dist/$gstatus_name.tar.gz" -C "dist/$gstatus_name" gstatus
done

if command -v sha256sum >/dev/null 2>&1; then
    (cd dist && sha256sum "gstatus-${gstatus_version}-"*.tar.gz > SHA256SUMS)
else
    (cd dist && shasum -a 256 "gstatus-${gstatus_version}-"*.tar.gz > SHA256SUMS)
fi
