#!/usr/bin/env bash
# Builds the Windows release artifacts into dist/. Called by semantic-release
# (@semantic-release/exec prepareCmd) with VERSION=<next version, no "v">.
set -euo pipefail
: "${VERSION:?VERSION required}"
rm -rf dist && mkdir dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o dist/zwiftboard-windows-amd64.exe .
(cd dist && zip -q zwiftboard-windows-amd64.zip zwiftboard-windows-amd64.exe)
(cd dist && sha256sum zwiftboard-windows-amd64.exe zwiftboard-windows-amd64.zip > checksums.txt)
