#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

gatus_version=${GATUS_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || printf dev)}
mkdir -p dist

for gatus_target in darwin/arm64 linux/arm64 linux/amd64; do
    gatus_os=${gatus_target%/*}
    gatus_arch=${gatus_target#*/}
    gatus_name="gatus-${gatus_version}-${gatus_os}-${gatus_arch}"

    mkdir -p "dist/$gatus_name"
    CGO_ENABLED=0 GOOS="$gatus_os" GOARCH="$gatus_arch" go build -trimpath -ldflags "-s -w -X main.version=$gatus_version" -o "dist/$gatus_name/gatus" ./cmd/gatus
    LC_ALL=C tar -czf "dist/$gatus_name.tar.gz" -C "dist/$gatus_name" gatus
done

if command -v sha256sum >/dev/null 2>&1; then
    (cd dist && sha256sum "gatus-${gatus_version}-"*.tar.gz > SHA256SUMS)
else
    (cd dist && shasum -a 256 "gatus-${gatus_version}-"*.tar.gz > SHA256SUMS)
fi
