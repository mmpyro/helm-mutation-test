#!/usr/bin/env sh
#
# Puts the plugin binary in bin/. Three paths, tried in this order:
#
#   1. A local checkout (`make install`, or `helm plugin install .`) builds from
#      source. Helm symlinks a directory install, so $HELM_PLUGIN_DIR being a
#      symlink is the signal. Downloading here would make the dev loop lie: you
#      would edit the tree, install it, and run the last release.
#   2. Otherwise download the release asset for plugin.yaml's version. This is the
#      path that makes `helm plugin install <url>` work with no Go toolchain, which
#      is the whole point of publishing binaries.
#   3. If the download fails and Go is present, build anyway rather than leaving
#      the user with nothing.
#
# Set HELM_MUTATION_TEST_BUILD=1 to force path 1 from anywhere.
set -eu

REPO=mmpyro/helm-mutation-test
BIN=bin/helm-mutation-test

manifest_version() {
  sed -n 's/^version: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' plugin.yaml
}

# Empty output means "no asset exists for this machine" — the caller falls back to
# building rather than guessing at a near-miss tarball.
platform() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    linux | darwin) ;;
    *) return 0 ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) return 0 ;;
  esac
  echo "${os}_${arch}"
}

fetch() { # fetch <url> <dest>
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    echo "Neither curl nor wget is available to download the release." >&2
    return 1
  fi
}

sha256() { # sha256 <file>
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# A missing checksum tool downgrades to a warning; a missing or mismatched entry
# does not. An asset we cannot account for is not one to install.
verify() { # verify <dir> <asset>
  actual="$(sha256 "$1/$2")"
  if [ -z "$actual" ]; then
    echo "Warning: no sha256sum or shasum found; skipping checksum verification." >&2
    return 0
  fi
  expected="$(awk -v a="$2" '$2 == a || $2 == "*" a {print $1}' "$1/SHA256SUMS")"
  if [ -z "$expected" ]; then
    echo "SHA256SUMS contains no entry for $2." >&2
    return 1
  fi
  if [ "$actual" != "$expected" ]; then
    echo "Checksum mismatch for $2: expected $expected, got $actual." >&2
    return 1
  fi
  return 0
}

download() { # download <version> <platform>
  asset="helm-mutation-test_${2}.tar.gz"
  base="https://github.com/${REPO}/releases/download/v${1}"
  tmp="$(mktemp -d)"
  status=1
  echo "Downloading ${asset} (v${1})..."
  if fetch "${base}/${asset}" "$tmp/$asset" &&
    fetch "${base}/SHA256SUMS" "$tmp/SHA256SUMS" &&
    verify "$tmp" "$asset"; then
    mkdir -p bin
    if tar -xzf "$tmp/$asset" -C bin helm-mutation-test; then
      chmod +x "$BIN"
      status=0
    fi
  fi
  rm -rf "$tmp"
  return $status
}

build() {
  command -v go >/dev/null 2>&1 || return 1
  echo "Building helm-mutation-test from source..."
  mkdir -p bin
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BIN" ./cmd/helm-mutation-test
}

if [ -L "${HELM_PLUGIN_DIR:-}" ] || [ "${HELM_MUTATION_TEST_BUILD:-}" = "1" ]; then
  if build; then
    echo "Installed $(pwd)/$BIN from this checkout"
    exit 0
  fi
  echo "This is a local checkout, which installs from source, but the Go toolchain" >&2
  echo "is missing (https://go.dev/dl/)." >&2
  exit 1
fi

version="$(manifest_version)"
platform="$(platform)"

if [ -z "$version" ]; then
  echo "Could not read 'version:' from plugin.yaml." >&2
elif [ -z "$platform" ]; then
  echo "No prebuilt binary is published for $(uname -s)/$(uname -m)." >&2
elif download "$version" "$platform"; then
  echo "Installed $(pwd)/$BIN (v${version}, ${platform})"
  exit 0
fi

echo "Falling back to building from source." >&2
if build; then
  echo "Installed $(pwd)/$BIN"
  exit 0
fi

cat >&2 <<'EOF'

Could not install helm-mutation-test. Either:
  - fix network access to github.com so the prebuilt binary can be downloaded, or
  - install the Go toolchain (https://go.dev/dl/) so it can be built from source.
EOF
exit 1
