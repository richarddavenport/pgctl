#!/usr/bin/env bash
# Print the CHANGELOG.md section for a version, or fail.
#
#   scripts/changelog.sh v0.1.0
#
# Failing is the point. A tag pushed with no entry should stop the release
# rather than publish generated commit titles for the ninth time — the tool
# this was taken from did that, and the real notes ended up written by hand
# afterwards, when they were written at all.
#
# One script, used by both scripts/release.sh and .github/workflows/release.yml,
# so a local release and a CI release cannot describe the same tag differently.
set -euo pipefail

version=${1:-}
file=${2:-CHANGELOG.md}
[ -n "$version" ] || { echo "usage: $(basename "$0") <version> [changelog]" >&2; exit 2; }
[ -f "$file" ] || { echo "$file not found" >&2; exit 1; }

# Both "## [0.1.0] - date" and "## 0.1.0" are accepted: the brackets are Keep a
# Changelog's link convention, not something a release should depend on.
notes=$(awk -v tag="${version#v}" '
  $0 ~ "^## \\[?" tag "\\]?([^0-9.]|$)" { found = 1; next }
  found && /^## / { exit }
  found { print }
' "$file")

# Trim leading and trailing blank lines, so the release body starts at the text.
notes=$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e :a -e '/^\n*$/{$d;N;};/\n$/ba')

if [ -z "$notes" ]; then
  echo "no $file section for $version — add one before releasing" >&2
  exit 1
fi
printf '%s\n' "$notes"
