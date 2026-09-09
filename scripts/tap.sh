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

# The release has to exist, and "not yet" is a different answer from "no".
#
# Pushing a tag starts the Release workflow, which takes a minute or two — so
# the obvious sequence, `git push origin v0.2.0 && make tap`, arrives here
# before there is anything to point at. The first version of this said "is not
# published", which reads as a failure when the truth is "wait". It waits now.
wait_for_release() {
  gh release view "$version" --repo "$repo" >/dev/null 2>&1 && return 0

  run=$(gh run list --repo "$repo" --workflow Release --limit 10 \
    --json databaseId,headBranch,status \
    --jq "[.[] | select(.headBranch == \"$version\" and .status != \"completed\")][0].databaseId" 2>/dev/null || true)
  if [ -z "$run" ] || [ "$run" = "null" ]; then
    echo "$version is not published in $repo, and no Release workflow is building it." >&2
    echo "  Push the tag to start one:  git push origin $version" >&2
    echo "  Or publish from here:       make release VERSION=$version" >&2
    return 1
  fi

  echo "waiting for the Release workflow to publish $version…"
  echo "  https://github.com/$repo/actions/runs/$run"
  # --exit-status makes a failed run a failed wait, which is the point: a
  # formula written against a release that never appeared is worse than no
  # formula.
  gh run watch "$run" --repo "$repo" --exit-status >/dev/null || {
    echo "the Release workflow for $version failed — nothing to point the tap at" >&2
    return 1
  }
  # The release object can lag the workflow's last step by a moment.
  for _ in 1 2 3 4 5 6; do
    gh release view "$version" --repo "$repo" >/dev/null 2>&1 && return 0
    sleep 5
  done
  echo "the workflow finished but $version still has no release" >&2
  return 1
}
wait_for_release || exit 1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git clone -q "$tap_url" "$work/tap"

# PGCTL_TAP_PUSHER stops update-tap.sh printing the commit-and-push instructions
# it ends with: they are right for somebody running it by hand and wrong here,
# where they name a temp directory this script is about to delete.
PGCTL_TAP_PUSHER=1 "$here/update-tap.sh" "$version" "$work/tap"

# Staged first, then compared against the index. `git diff` alone does not see
# an UNTRACKED file, so the first version of this reported "the formula already
# describes v0.2.0 — nothing to push" for a formula it had just created, and
# pushed nothing. The tap's first formula is exactly the case that has to work.
git -C "$work/tap" add Formula/pgctl.rb
if git -C "$work/tap" diff --cached --quiet; then
  echo "the formula already describes $version — nothing to push"
  exit 0
fi
git -C "$work/tap" commit -qm "pgctl $version"
git -C "$work/tap" push -q

echo
echo "tap updated: brew update && brew info $tap_repo/pgctl"
