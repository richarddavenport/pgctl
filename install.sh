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

# Public releases need nothing but curl. gh is the fallback, not the
# requirement: while the repository was private a plain curl got a 404 that
# looked exactly like "no such release", and gh is what carries the token and
# reports the real error. Trying curl first means the common case — somebody who
# found the tool and has no gh — works.
fetch() {
	if command -v curl >/dev/null; then
		curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null; then
		wget -qO "$2" "$1"
	else
		return 1
	fi
}

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

base="https://github.com/$REPO/releases"
if [ "$VERSION" = latest ]; then
	# `releases/latest/download/<asset>` redirects, so the tag never has to be
	# resolved first. The version is read back off the binary at the end.
	url="$base/latest/download"
else
	url="$base/download/$VERSION"
fi

echo "fetching $asset from $VERSION"
if ! fetch "$url/$asset" "$tmp/$asset" || ! fetch "$url/checksums.txt" "$tmp/checksums.txt"; then
	# Either there is no such release, or the repository is private and this
	# download was a 404 that means "not authorised". gh can tell the
	# difference, so hand over to it rather than guessing.
	if ! command -v gh >/dev/null; then
		echo "install.sh: could not download $asset from $VERSION" >&2
		echo "  if $REPO is private, this needs the gh CLI:" >&2
		echo "  brew install gh && gh auth login" >&2
		exit 1
	fi
	if [ "$VERSION" = latest ]; then
		VERSION="$(gh release view --repo "$REPO" --json tagName -q .tagName)"
	fi
	gh release download "$VERSION" --repo "$REPO" \
		--pattern "$asset" --pattern checksums.txt --dir "$tmp"
fi

# The checksum is worth verifying even over HTTPS: it
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
