# zwiftboard tasks. Run `just` to list.

default:
    @just --list

# --- checks ---------------------------------------------------------------

test:
    go test -race ./...

vet:
    go vet ./...
    GOOS=windows go vet ./...

fmt:
    gofmt -w .

fmt-check:
    #!/bin/sh
    out=$(gofmt -l .)
    if [ -n "$out" ]; then
        echo "gofmt needed on:"; echo "$out"; exit 1
    fi

# --- build ----------------------------------------------------------------

# Build the Windows exe icon resource (generated, never committed).
gen-icon:
    go run github.com/tc-hib/go-winres@v0.3.3 simply \
        --manifest none --arch amd64 --out rsrc \
        --icon res/icon/zwiftboard-256.png

build: gen-icon
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
        -ldflags="-s -w" \
        -o dist/zwiftboard.exe .

# Local: formats, then checks + builds.
sanity: fmt test vet build

# CI: check-only, no rewrites.
ci: fmt-check vet test build

# --- release (needs zip + sha256sum: Ubuntu CI + local) -------------------

release version:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf dist && mkdir dist
    just gen-icon
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
        -ldflags="-s -w -X main.version={{version}}" \
        -o dist/zwiftboard.exe .
    (cd dist && zip -q zwiftboard.zip zwiftboard.exe)
    (cd dist && sha256sum zwiftboard.exe zwiftboard.zip > checksums.txt)

# Build the native Apple Silicon macOS release. This must run on macOS because
# CoreBluetooth is linked through cgo.
release-macos version:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf dist/macos && mkdir -p dist/macos
    GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath \
        -ldflags="-s -w -X main.version={{version}}" \
        -o dist/macos/zwiftboard .
    (cd dist/macos && zip -q zwiftboard-macos-arm64.zip zwiftboard)
    (cd dist/macos && shasum -a 256 zwiftboard zwiftboard-macos-arm64.zip > checksums-macos.txt)

# --- scoop (local + CI) ---------------------------------------------------

# Render the ScoopInstaller/Extras manifest for VERSION + exe SHA256 to stdout.
scoop-manifest version hash:
    #!/usr/bin/env bash
    # Same recipe runs locally and in the release-workflow scoop job. VERSION is
    # the bare semantic-release tag (no "v"); SHA256 is the released
    # zwiftboard.exe hash from that release's checksums.txt asset.
    set -euo pipefail
    version='{{version}}'
    hash='{{hash}}'
    cat <<EOF
    {
        "version": "$version",
        "description": "Turn a Zwift Click V2 controller into a Bluetooth keyboard for indoor cycling apps or any apps.",
        "homepage": "https://github.com/thomaschampagne/zwiftboard",
        "license": "MIT",
        "architecture": {
            "64bit": {
                "url": "https://github.com/thomaschampagne/zwiftboard/releases/download/$version/zwiftboard.exe",
                "hash": "$hash"
            }
        },
        "bin": "zwiftboard.exe",
        "shortcuts": [["zwiftboard.exe", "zwiftboard"]],
        "checkver": "github",
        "autoupdate": {
            "architecture": {
                "64bit": {
                    "url": "https://github.com/thomaschampagne/zwiftboard/releases/download/\$version/zwiftboard.exe"
                }
            },
            "hash": {
                "url": "\$baseurl/checksums.txt"
            }
        }
    }
    EOF

# Local end-to-end: build the release assets and render dist/zwiftboard.json.
scoop-release version: (release version)
    #!/usr/bin/env bash
    # Exercises the whole scoop pipeline before a live main push. POSIX-only
    # (zip/sha256sum), like `release`.
    set -euo pipefail
    sha=$(awk '$2 == "zwiftboard.exe" { print $1 }' dist/checksums.txt)
    [ -n "$sha" ] || { echo "no zwiftboard.exe hash in dist/checksums.txt"; exit 1; }
    just scoop-manifest {{version}} "$sha" > dist/zwiftboard.json
    echo "wrote dist/zwiftboard.json"

# --- deps -----------------------------------------------------------------

# Blanket dependency upgrade. Upgrades tinygo BLE libs too: hardware re-verify
# before a release that includes BLE dep bumps.
update-deps:
    go get -u ./...
    go mod tidy

# --- dev ------------------------------------------------------------------

run:
    go run .

demo:
    go run . -demo
