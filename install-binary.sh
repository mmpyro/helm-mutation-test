#!/usr/bin/env sh
#
# Builds the plugin binary from source into bin/.
#
# Building rather than downloading keeps the install honest while the project has
# no tagged releases: there is nothing to fetch yet, and a script that silently
# fell back to a stale binary would be worse than one that needs a toolchain.
set -eu

if ! command -v go >/dev/null 2>&1; then
  echo "helm-mutation-test needs the Go toolchain to build (https://go.dev/dl/)." >&2
  exit 1
fi

mkdir -p bin
echo "Building helm-mutation-test..."
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/helm-mutation-test ./cmd/helm-mutation-test
echo "Installed $(pwd)/bin/helm-mutation-test"
