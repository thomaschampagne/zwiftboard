#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

gofmt -w .
go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
