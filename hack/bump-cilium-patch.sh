#!/bin/sh
# Move the Cilium chart Ichor embeds to the newest patch of the minor it already pins.
#
# Ichor pins the Cilium version: a cluster's Cilium only moves to the chart embedded in the running
# Ichor, so an upstream patch reaches users through an Ichor release. This script turns a new upstream
# patch into a small, reviewable change (CI runs it daily and opens a pull request, see
# .github/workflows/cilium-patch.yml and CONTRIBUTING.md).
#
# It reads CILIUM_CHART_VERSION from the Makefile, looks for a newer final (not -rc/-pre) release of the
# SAME minor in the Cilium Helm index, and never crosses a minor. Before it writes anything it
#   - checks the chart archive against the digest in the index,
#   - verifies, with cosign, that the agent, operator-generic and hubble-relay images the chart pins are
#     signed by the Cilium release workflow of that tag (the identity internal/imagesig uses), and
#   - checks that the Kubernetes versions Cilium tests that tag with are the ones pkg/release/compat.go
#     states for the minor.
# Then it moves every version pin (listed below), fetches the chart with `make cilium-chart` (which
# verifies the SHA-256 again) and regenerates the golden manifests with `make cilium-goldens`.
#
# Exit 0 with "no newer patch" when up to date. Needs curl, yq (mikefarah), cosign, tar, sha256sum, make,
# go: run it inside `nix develop`.
set -eu

cd "$(dirname "$0")/.."

helm_base=https://helm.cilium.io
oidc_issuer=https://token.actions.githubusercontent.com
workflow=https://github.com/cilium/cilium/.github/workflows/build-images-releases.yaml@refs/tags/
k8s_matrix=.github/actions/ginkgo/main-k8s-versions.yaml
chart_dir=pkg/cni/cilium/chart

die() {
	echo "bump-cilium-patch: $*" >&2
	exit 1
}

for tool in curl yq cosign tar sha256sum sort make go; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool not found (run inside nix develop)"
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

makefile_var() {
	sed -n "s/^$1 ?= //p" Makefile
}

old=$(makefile_var CILIUM_CHART_VERSION)
echo "$old" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || die "CILIUM_CHART_VERSION in the Makefile is not X.Y.Z: '$old'"
minor=${old%.*}

curl -fsSL -o "$work/index.yaml" "$helm_base/index.yaml" || die "cannot fetch $helm_base/index.yaml"

# Final releases of the pinned minor only: the pattern rejects 1.20.0-rc.1 and the like.
new=$(m=$minor yq '.entries.cilium[].version
	| select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
	| select(sub("\\.[0-9]+$"; "") == strenv(m))' "$work/index.yaml" | sort -V | tail -n 1)
[ -n "$new" ] || die "the Helm index lists no final release of Cilium $minor"

if [ "$new" = "$old" ] || [ "$(printf '%s\n%s\n' "$old" "$new" | sort -V | tail -n 1)" = "$old" ]; then
	echo "no newer patch: the pinned Cilium chart $old is the newest of $minor ($new upstream)"
	exit 0
fi

echo "Cilium $minor: pinned $old, newest upstream $new"

digest=$(v=$new yq '.entries.cilium[] | select(.version == strenv(v)) | .digest' "$work/index.yaml")
echo "$digest" | grep -Eq '^[0-9a-f]{64}$' || die "the Helm index has no SHA-256 digest for $new: '$digest'"

archive="$work/cilium-$new.tgz"
curl -fsSL -o "$archive" "$helm_base/cilium-$new.tgz" || die "cannot fetch the chart $new"
echo "$digest  $archive" | sha256sum -c - >/dev/null || die "the chart $new does not match the digest in the Helm index"
[ "$(tar -xzOf "$archive" cilium/Chart.yaml | yq '.version')" = "$new" ] || die "the archive is not chart $new"

# The images the chart pins, as Ichor verifies them (internal/controller/release_check.go).
tar -xzOf "$archive" cilium/values.yaml >"$work/values.yaml"
for image in \
	"quay.io/cilium/cilium .image.digest" \
	"quay.io/cilium/operator-generic .operator.image.genericDigest" \
	"quay.io/cilium/hubble-relay .hubble.relay.image.digest"; do
	repo=${image% *}
	img_digest=$(yq "${image#* }" "$work/values.yaml")
	echo "$img_digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || die "the chart has no digest for $repo: '$img_digest'"
	echo "verifying the signature of $repo@$img_digest"
	cosign verify --certificate-oidc-issuer "$oidc_issuer" --certificate-identity "$workflow""v$new" \
		"$repo@$img_digest" >/dev/null 2>"$work/cosign.err" ||
		{
			cat "$work/cosign.err" >&2
			die "$repo@$img_digest is not signed by the Cilium v$new release workflow: nothing was changed"
		}
done

# pkg/release/compat.go states the Kubernetes range Cilium tests this minor with, from the release's
# own matrix. A patch that changes the matrix needs a human to read it.
k8s_list() {
	curl -fsSL "https://raw.githubusercontent.com/cilium/cilium/v$1/$k8s_matrix" | yq '[.include[]."k8s-version"] | sort | join(" ")'
}
[ "$(k8s_list "$old")" = "$(k8s_list "$new")" ] ||
	die "Cilium v$new tests other Kubernetes versions than v$old ($k8s_matrix); update pkg/release/compat.go by hand: nothing was changed"

# Everything is checked: write.
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
[ "$old" = "$(makefile_var FOREIGN_CILIUM_CHART_VERSION)" ] || rm -f "$chart_dir/cilium-$old.tgz"

# 3. Prose and hardware-test checks that name the embedded version. Not touched: CHANGELOG.md (a release
#    section is written when the release is cut) and test fixtures that merely use a Cilium version.
#    The pattern keeps 1.20.45 and 21.20.4 out; it runs twice because neighbours share a delimiter.
for f in README.md docs/guides/getting-started.md docs/guides/adopt-and-reclaim.md \
	test/e2e/foreign/cilium_test.go test/internal/foreign/foreign.go; do
	for _ in 1 2; do
		subst "$f" "s/\([^0-9.]\|^\)$oldre\([^0-9]\|\$\)/\1$new\2/g"
	done
done
subst pkg/release/compat.go "s#cilium/cilium/blob/v$oldre/$k8s_matrix#cilium/cilium/blob/v$new/$k8s_matrix#"

# 4. The chart itself, digest-verified by the Makefile, and what is rendered from it.
make cilium-chart
make cilium-goldens

echo "moved the embedded Cilium chart from $old to $new"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
	{
		echo "old=$old"
		echo "new=$new"
	} >>"$GITHUB_OUTPUT"
fi
