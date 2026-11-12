#!/usr/bin/env bash
# add-release-section.sh TAG SRC: copy the "## TAG (date)" section of the changelog file SRC (the one at the
# tag) into ./CHANGELOG.md, newest first: before the first section of an older version, or at the end if there
# is none. The body is cut with the matcher release.yml uses for the release notes. Does nothing when
# ./CHANGELOG.md already has a section for TAG, so a rerun, or a section written by hand, is never overwritten
# or duplicated.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: add-release-section.sh vX.Y.Z SRC" >&2; exit 2; }
tag=$1 src=$2
[[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo "add-release-section: TAG must be a final semver tag like v0.1.0 (got '$tag')" >&2; exit 2; }
if grep -q "^## ${tag} " CHANGELOG.md; then echo "CHANGELOG.md already has a section for ${tag}; leaving it alone"; exit 0; fi

notes=$(mktemp)
trap 'rm -f "$notes"' EXIT
grep -m1 "^## ${tag} " "$src" > "$notes" || { echo "add-release-section: $src has no section starting '## ${tag} '" >&2; exit 1; }
awk -v h="## ${tag} " 'index($0, "## ") == 1 { p = index($0, h) == 1; next } p' "$src" >> "$notes"

# older(a, b): a < b as X.Y.Z. A heading that is not a final version ("## Unreleased") is never older, so it stays on top.
awk -v tag="$tag" -v notes="$notes" '
function older(a, b,   x, y, i) {
  if (a !~ /^v[0-9]+\.[0-9]+\.[0-9]+$/) return 0
  split(substr(a, 2), x, "."); split(substr(b, 2), y, ".")
  for (i = 1; i <= 3; i++) if (x[i] + 0 != y[i] + 0) return x[i] + 0 < y[i] + 0
  return 0
}
function emit(   i) { for (i = 1; i <= n; i++) print body[i]; print ""; done = 1 }
BEGIN {
  while ((getline line < notes) > 0) body[++n] = line
  while (n > 0 && body[n] == "") n--
}
index($0, "## ") == 1 && !done { split($0, w, " "); if (older(w[2], tag)) emit() }
{ print }
END { if (!done) { print ""; for (i = 1; i <= n; i++) print body[i] } }
' CHANGELOG.md > CHANGELOG.md.new
mv CHANGELOG.md.new CHANGELOG.md
