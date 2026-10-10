#!/bin/sh
# Builds a kijk release into dist/: scripts/release.sh 0.3.0
# It runs on a Mac because the macOS binaries need cgo: without it the file watcher falls back
# to one file handle per file.
set -eu

fail() { printf 'release: %s\n' "$*" >&2; exit 1; }

v="${1:-}"
printf '%s' "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "usage: scripts/release.sh X.Y.Z (no leading v)"
[ "$(uname -s)" = Darwin ] || fail "build releases on a Mac, so the macOS binaries get cgo"

cd "$(dirname "$0")/.."
rm -rf dist
mkdir dist

build() {
  name="kijk-$1-$2"
  [ "$1" = windows ] && name="$name.exe"
  printf '  %s\n' "$name"
  CGO_ENABLED="$3" GOOS="$1" GOARCH="$2" go build -trimpath -ldflags "-s -w -X main.version=$v" -o "dist/$name" .
}

echo "Building kijk $v"
build darwin arm64 1
build darwin amd64 1
build linux amd64 0
build linux arm64 0
build windows amd64 0
build windows arm64 0
cp install.sh install.ps1 LICENSE dist/
(cd dist && shasum -a 256 kijk-* > checksums.txt)
echo "Done: the release files are in dist/"
