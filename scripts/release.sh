#!/usr/bin/env bash
# Cut a release from a local checkout: check, build, publish, point the tap at
# it. One command, no CI, no secrets beyond your own gh login.
#
#   scripts/release.sh v0.1.0
#   scripts/release.sh v0.1.0 --dry-run
#
# .github/workflows/release.yml does the same thing when GitHub is allocating
# runners. This exists because it frequently is not — and because a release that
# only a working CI can produce is a release you cannot ship on a bad day.
#
# What it does NOT do: decide the version, write the notes, or push the tag for
# you. Read the diff, pick the number, write the CHANGELOG entry, tag it, then
# run this.
set -euo pipefail

version=${1:-}
dry=false
[ "${2:-}" = "--dry-run" ] && dry=true

usage() { echo "usage: $(basename "$0") <version> [--dry-run]" >&2; exit 2; }
[ -n "$version" ] || usage
case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "version must look like v0.1.0, got $version" >&2; exit 2 ;;
esac

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
repo=richarddavenport/pgctl
tap_repo=richarddavenport/homebrew-tap
# Clone the tap over ssh, not https. https needs a git credential helper holding
# a valid token, which is a second credential to keep working — and when it
# rotted in the tool this came from, the release published and then failed to
# push the formula, leaving `update` on the new version while brew served the
# old one. ssh is the transport this repo is already cloned with, so if you can
# push pgctl you can push the tap.
tap_url=${PGCTL_TAP_URL:-git@github.com:$tap_repo}

say() { printf '\n== %s\n' "$*"; }

# ---------------------------------------------------------------------------
# Refuse to ship something nobody has checked.
# ---------------------------------------------------------------------------
say "checks"
[ -z "$(git status --porcelain)" ] || { echo "working tree is dirty — commit or stash first" >&2; exit 1; }

branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = master ] || echo "warning: releasing from $branch, not master"

# The tag must already exist and point at HEAD. Creating it here would mean a
# typo in the version silently tags the wrong thing.
tagged=$(git rev-parse -q --verify "refs/tags/$version^{commit}" || true)
[ -n "$tagged" ] || {
  echo "no tag $version — create it first:" >&2
  echo "  git tag -a $version -m 'pgctl $version' && git push origin $version" >&2
  exit 1
}
[ "$tagged" = "$(git rev-parse HEAD)" ] || { echo "tag $version is not at HEAD" >&2; exit 1; }

if gh release view "$version" --repo "$repo" >/dev/null 2>&1; then
  echo "$version is already published in $repo — nothing to do" >&2
  exit 1
fi

# The same checks `make check` runs, because a release is the one build nobody
# gets to re-run.
make check >/dev/null

# The notes, before anything is built. A tag with no CHANGELOG entry stops here.
notes=$("$repo_root/scripts/changelog.sh" "$version") || exit 1

# The tap is checked BEFORE anything is published. A release is two writes —
# assets to the repo, then the formula to the tap — and discovering the second
# cannot proceed after the first has landed leaves `pgctl update` on the new
# version while brew still serves the old one.
#
# Over ssh, ls-remote authenticates, so reaching the repo proves the credential
# works. It would prove nothing over https: anonymous read succeeds on a public
# repo and only the push fails, which is exactly how this went unnoticed before.
git ls-remote --exit-code "$tap_url" HEAD >/dev/null 2>&1 || {
  echo "cannot reach the tap at $tap_url" >&2
  echo "  nothing has been published. Fix that remote's auth, or point" >&2
  echo "  PGCTL_TAP_URL at one that works." >&2
  exit 1
}
# Push permission is a separate question from transport, so ask the API too —
# advisory only: an unavailable or rate-limited API must not block a release.
tap_push=$(gh api "repos/$tap_repo" --jq '.permissions.push' 2>/dev/null || true)
[ "$tap_push" = "false" ] && {
  echo "your account cannot push to $tap_repo — nothing has been published" >&2
  exit 1
}
echo "  clean tree, tag at HEAD, make check, notes, tap reachable"

# ---------------------------------------------------------------------------
# Build, with the same flags the release workflow uses.
# ---------------------------------------------------------------------------
say "building $version"
sha=$(git rev-parse HEAD)
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
for os in darwin linux; do
  for arch in arm64 amd64; do
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
      -ldflags "-s -w -X main.version=$version -X main.commit=${sha:0:9}" \
      -o "$out/pgctl-$os-$arch" ./cmd/pgctl
    echo "  $os/$arch  $(du -h "$out/pgctl-$os-$arch" | cut -f1)"
  done
done
(cd "$out" && shasum -a 256 pgctl-* > checksums.txt)

# A binary that cannot report its own version is a binary built wrong, and the
# native one is the only one this machine can run.
native_os=$(uname -s | tr '[:upper:]' '[:lower:]')
native_arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
reported=$("$out/pgctl-$native_os-$native_arch" version)
case "$reported" in
  *"$version"*) echo "  reports: $reported" ;;
  *) echo "built binary reports '$reported', expected $version" >&2; exit 1 ;;
esac

if $dry; then
  say "dry run — would publish to $repo and update $tap_url"
  echo "  notes: $(printf '%s' "$notes" | head -1)"
  sed 's/^/  /' "$out/checksums.txt"
  exit 0
fi

# ---------------------------------------------------------------------------
# Publish. This is the step `pgctl update` and install.sh read from.
# ---------------------------------------------------------------------------
say "publishing to $repo"
# The changelog section is the release body; the install lines follow it, since
# "what changed" is what somebody opening a release came to read.
notes_file=$out/notes.md
{
  printf '%s\n\n' "$notes"
  printf '%s\n' \
    "---" \
    "" \
    "Install:" \
    '```sh' \
    "brew install richarddavenport/tap/pgctl" \
    "# or" \
    "curl -fsSL https://raw.githubusercontent.com/$repo/master/install.sh | bash" \
    '```' \
    "" \
    "Update an existing install with \`pgctl update\`, or \`U\` in the interface." \
    "" \
    "Built from ${sha:0:9}."
} > "$notes_file"
gh release create "$version" "$out"/pgctl-* "$out/checksums.txt" --repo "$repo" \
  --title "pgctl $version" \
  --notes-file "$notes_file"

# ---------------------------------------------------------------------------
# The tap, from the published release rather than the local files, so a
# mismatch between what was uploaded and what brew will fetch cannot pass
# unnoticed.
# ---------------------------------------------------------------------------
say "updating the tap"
tap=$(mktemp -d)
git clone -q "$tap_url" "$tap"
"$repo_root/scripts/update-tap.sh" "$version" "$tap"
git -C "$tap" add Formula/pgctl.rb
git -C "$tap" commit -qm "pgctl $version"
git -C "$tap" push -q
rm -rf "$tap"

say "done"
echo "  release: https://github.com/$repo/releases/tag/$version"
echo "  anyone on an older build can now run: pgctl update"
echo
echo "verify:"
echo "  brew update && brew info richarddavenport/tap/pgctl"
echo "  BIN=/tmp/pgctl-check bash <(curl -fsSL https://raw.githubusercontent.com/$repo/master/install.sh)"
