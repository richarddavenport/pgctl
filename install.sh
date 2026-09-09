#!/usr/bin/env bash
# Fetch the pgctl release binary for this platform and put it on your PATH.
#
# For someone who wants the tool. If you want to CHANGE it, clone the repo and
# run `make install`: tuikit is a tagged public module, so `go build` needs
# nothing beside the checkout.
#
#   curl -fsSL <this file> | bash
#   VERSION=v0.2.0 ./install.sh     # a specific release
#   BIN=/usr/local/bin/pgctl ./install.sh
set -euo pipefail

REPO="${REPO:-richarddavenport/pgctl}"
BIN="${BIN:-$HOME/.local/bin/pgctl}"
VERSION="${VERSION:-latest}"

# The source repository is private, so the release assets are too, and a plain
# curl gets a 404 that looks exactly like "no such release". gh carries the
# token this needs and reports the real error, so it is the requirement rather
# than something to work around. Publishing from a separate public dist repo —
# what swarmctl does, and issue #5 has to settle the licence first — is what
# removes it.
if ! command -v gh >/dev/null; then
	echo "install.sh: needs the gh CLI, because $REPO is private" >&2
	echo "  brew install gh && gh auth login" >&2
	exit 1
fi

case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux)  os=linux ;;
	*) echo "install.sh: no build for $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
	arm64|aarch64) arch=arm64 ;;
	x86_64|amd64)  arch=amd64 ;;
	*) echo "install.sh: no build for $(uname -m)" >&2; exit 1 ;;
esac

asset="pgctl-$os-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ "$VERSION" = latest ]; then
	VERSION="$(gh release view --repo "$REPO" --json tagName -q .tagName)"
fi
echo "fetching $asset from $VERSION"
gh release download "$VERSION" --repo "$REPO" \
	--pattern "$asset" --pattern checksums.txt --dir "$tmp"

# The checksum is worth verifying even over HTTPS from a private repo: it
# catches a truncated download, which is the failure that produces a binary
# that runs and then does something surprising.
( cd "$tmp" && grep " $asset\$" checksums.txt | shasum -a 256 -c - >/dev/null )

mkdir -p "$(dirname "$BIN")"
install -m 755 "$tmp/$asset" "$BIN"

echo "installed $("$BIN" version) -> $BIN"
case ":$PATH:" in
	*":$(dirname "$BIN"):"*) ;;
	*) echo "note: $(dirname "$BIN") is not on your PATH" ;;
esac
