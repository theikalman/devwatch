#!/bin/sh
# Install the latest devwatch release (macOS Apple Silicon).
#   curl -fsSL https://raw.githubusercontent.com/theikalman/devwatch/main/install.sh | sh
# Optional env: VERSION=v0.1.0 to pin a release, BIN_DIR=/usr/local/bin to change the target.
set -eu

REPO=theikalman/devwatch
BIN_DIR=${BIN_DIR:-$HOME/.local/bin}
asset=devwatch_darwin_arm64.tar.gz

if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
	echo "devwatch only ships macOS arm64 builds for now" >&2
	exit 1
fi

if [ -n "${VERSION:-}" ]; then
	base=https://github.com/$REPO/releases/download/$VERSION
else
	base=https://github.com/$REPO/releases/latest/download
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/$asset.sha256" -o "$tmp/$asset.sha256"
(cd "$tmp" && shasum -a 256 -c "$asset.sha256")
tar -xzf "$tmp/$asset" -C "$tmp"

mkdir -p "$BIN_DIR"
install -m 755 "$tmp/devwatch" "$BIN_DIR/devwatch"
echo "installed $BIN_DIR/devwatch"

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) echo "note: $BIN_DIR is not on your PATH; add it to your shell profile" ;;
esac
