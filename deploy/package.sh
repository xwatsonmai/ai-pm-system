#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
release="${1:-$(date +%Y%m%d-%H%M%S)}"
case "$release" in *[!a-zA-Z0-9._-]*|'') echo 'Invalid release name' >&2; exit 1;; esac
dest="bin/releases/$release"
test ! -e "$dest"
mkdir -p "$dest/runtime"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$dest/ai-pm" ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$dest/ai-pm-sandbox-runner" ./cmd/sandbox-runner
cp runtime/package.json runtime/package-lock.json runtime/*.mjs "$dest/runtime/"
cp deploy/Dockerfile deploy/Worker.Dockerfile deploy/compose.yaml deploy/compose.sandbox.yaml deploy/ai-pm-sandbox.service "$dest/"
cp deploy/update.py "$dest/"
(
  cd "$dest"
  shasum -a 256 ai-pm ai-pm-sandbox-runner runtime/* Dockerfile Worker.Dockerfile compose*.yaml ai-pm-sandbox.service update.py > SHA256SUMS
)
tar -czf "$dest.tar.gz" -C "$dest" .
printf '%s\n' "$dest.tar.gz"
