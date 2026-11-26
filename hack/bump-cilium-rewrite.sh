#!/bin/sh
# Rewrite every pin of the embedded Cilium chart from OLD to NEW and regenerate what derives from it.
# Usage: hack/bump-cilium-rewrite.sh OLD NEW CHART.tgz
#
# The second half of hack/bump-cilium-patch.sh, which finds NEW, fetches CHART.tgz and verifies its digest and
# the image signatures. This half touches no network (make third-party-notices reads the module cache and
# ui/package-lock.json), so make check-cilium-bump can run it on a made-up patch in a throwaway copy.
set -eu

cd "$(dirname "$0")/.."

[ $# -eq 3 ] || { echo "usage: $0 OLD NEW CHART.tgz" >&2; exit 2; }
old=$1
new=$2
archive=$3

k8s_matrix=.github/actions/ginkgo/main-k8s-versions.yaml
chart_dir=pkg/cni/cilium/chart
# The docs TestDocsStateThePinnedVersions skips (docsSkipped in pkg/release/docs_test.go, which a test keeps equal
# to this list): top-level directories of docs/ only.
docs_skipped="adr design reference"

die() {
	echo "bump-cilium-rewrite: $*" >&2
	exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

makefile_var() {
	sed -n "s/^$1 ?= //p" Makefile
}

digest=$(sha256sum "$archive" | cut -d' ' -f1)

subst() {
	sed "$2" "$1" >"$work/subst" && cat "$work/subst" >"$1"
}
# must_have FILE TEXT: the move took effect.
must_have() {
	grep -Fq -- "$2" "$1" || die "$1: expected '$2' after the bump, the file's layout changed"
}

oldre=$(printf '%s' "$old" | sed 's/\./\\./g')
echo "moving the pins from $old to $new"

# 1. The pin and its digest (make cilium-chart reads both).
subst Makefile "s/^CILIUM_CHART_VERSION ?= .*/CILIUM_CHART_VERSION ?= $new/"
subst Makefile "s/^CILIUM_CHART_SHA256 ?= .*/CILIUM_CHART_SHA256 ?= $digest/"
must_have Makefile "CILIUM_CHART_SHA256 ?= $digest"

# 2. The embedded archive and the version constant the code compares with.
subst pkg/cni/cilium/render.go "s#^//go:embed chart/cilium-$oldre\.tgz#//go:embed chart/cilium-$new.tgz#"
must_have pkg/cni/cilium/render.go "//go:embed chart/cilium-$new.tgz"
subst pkg/cni/cilium/values.go "s/^\([[:space:]]*ChartVersion = \)\"$oldre\"/\1\"$new\"/"
must_have pkg/cni/cilium/values.go "ChartVersion = \"$new\""
# The embedded archive is the one chart .gitignore does not ignore (the foreign test chart is fetched, not tracked).
subst .gitignore "s#^!/pkg/cni/cilium/chart/cilium-$oldre\.tgz#!/pkg/cni/cilium/chart/cilium-$new.tgz#"
must_have .gitignore "!/pkg/cni/cilium/chart/cilium-$new.tgz"
[ "$old" = "$(makefile_var FOREIGN_CILIUM_CHART_VERSION)" ] || rm -f "$chart_dir/cilium-$old.tgz"

# 3. Prose and hardware-test checks that name the embedded version. Not touched: CHANGELOG.md (a release
#    section is written when the release is cut) and test fixtures that merely use a Cilium version.
#    The pattern keeps 1.20.45 and 21.20.4 out; it runs twice because neighbours share a delimiter.
# The docs are every file under docs/ that TestDocsStateThePinnedVersions scans: not the top-level $docs_skipped.
# shortcut: a file-wide grep, so a prose line that names the old version for another reason is moved too; narrow it then.
prune=
for d in $docs_skipped; do prune="$prune -path docs/$d -o"; done
# /dev/null keeps grep from reading stdin when find lists nothing
# shellcheck disable=SC2046
for f in README.md $(grep -lF -e "$old" /dev/null $(find docs \( $prune -false \) -prune -o -type f -name '*.md' -print)) \
	test/e2e/foreign/cilium_test.go test/internal/foreign/foreign.go; do
	for _ in 1 2; do
		subst "$f" "s/\([^0-9.]\|^\)$oldre\([^0-9]\|\$\)/\1$new\2/g"
	done
done
subst pkg/release/compat.go "s#cilium/cilium/blob/v$oldre/$k8s_matrix#cilium/cilium/blob/v$new/$k8s_matrix#"

# 4. The chart itself, digest-verified by the Makefile, and what is rendered from it.
# The archive was fetched and verified against the Helm index digest by hack/bump-cilium-patch.sh.
cp "$archive" "$chart_dir/cilium-$new.tgz"
echo "$digest  $chart_dir/cilium-$new.tgz" | sha256sum -c - >/dev/null || die "the archive does not match its digest"
make cilium-goldens
# THIRD-PARTY-NOTICES names the embedded archive and chart version; TestEmbeddedFilesAreInNotices reads it.
make third-party-notices

echo "moved the embedded Cilium chart from $old to $new"
