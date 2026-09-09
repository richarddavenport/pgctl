#!/usr/bin/env bash
# Point the Homebrew tap at a published release: clone, regenerate the formula,
# commit, push.
#
#   scripts/tap.sh v0.2.0
#
# Separate from release.sh, and reachable on its own, because there are two ways
# a release gets published and only one of them ends here. Pushing a tag runs
# .github/workflows/release.yml, which builds and publishes the binaries and
# knows nothing about Homebrew; release.sh does both and then refuses to run
# again for a version already published. Without this script, taking the
# ordinary route — push the tag, let CI do it — left the tap on the previous
# version with nothing to say so.
set -euo pipefail

version=${1:-}
[ -n "$version" ] || { echo "usage: $(basename "$0") <version>" >&2; exit 2; }
case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "version must look like v0.2.0, got $version" >&2; exit 2 ;;
esac

here=$(cd "$(dirname "$0")" && pwd)
repo=richarddavenport/pgctl
tap_repo=richarddavenport/homebrew-tap
# ssh, for the reason release.sh gives: over https an anonymous read succeeds on
# a public repo and only the push fails, so reaching it proves nothing.
tap_url=${PGCTL_TAP_URL:-git@github.com:$tap_repo}

gh release view "$version" --repo "$repo" >/dev/null 2>&1 || {
  echo "$version is not published in $repo — the formula would point at nothing" >&2
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git clone -q "$tap_url" "$work/tap"

"$here/update-tap.sh" "$version" "$work/tap"

if git -C "$work/tap" diff --quiet -- Formula/pgctl.rb; then
  echo "the formula already describes $version — nothing to push"
  exit 0
fi
git -C "$work/tap" add Formula/pgctl.rb
git -C "$work/tap" commit -qm "pgctl $version"
git -C "$work/tap" push -q

echo
echo "tap updated: brew update && brew info $tap_repo/pgctl"
