#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

out=$(gofmt -l .)
if [ -n "$out" ]; then
  echo "gofmt needed on:" && echo "$out"
  exit 1
fi

go vet ./...
go test ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
