#!/bin/sh
# Run hack/bump-cilium-rewrite.sh on a made-up next patch (X.Y.Z -> X.Y.99) in a throwaway copy of the
# working tree, then the tests the bot's pull request has to pass. Needs no network.
set -eu

cd "$(dirname "$0")/.."

old=$(sed -n 's/^CILIUM_CHART_VERSION ?= //p' Makefile)
new=${old%.*}.99
copy=$(mktemp -d)
trap 'rm -rf "$copy"' EXIT

# The working tree, tracked and new files, as it is now: git archive would miss uncommitted changes.
# shortcut: bin/ is shared by symlink so the copy reuses the installed tools; drop it if a test starts writing there.
git ls-files -co --exclude-standard -z | tar --null -T - -cf - | tar -xf - -C "$copy"
# chart archives are gitignored but embedded
cp pkg/cni/cilium/chart/cilium-"$old".tgz "$copy/pkg/cni/cilium/chart/"
ln -s "$PWD/bin" "$copy/bin"
[ ! -d ui/node_modules ] || ln -s "$PWD/ui/node_modules" "$copy/ui/node_modules"
# The made-up patch is the current chart with its own version moved, as TestEmbeddedChartMatchesPinnedVersion reads it.
unpacked=$(mktemp -d)
trap 'rm -rf "$copy" "$unpacked"' EXIT
tar -xzf "pkg/cni/cilium/chart/cilium-$old.tgz" -C "$unpacked"
sed -i "s/^version: $old\$/version: $new/" "$unpacked/cilium/Chart.yaml"
grep -q "^version: $new\$" "$unpacked/cilium/Chart.yaml" || { echo "check-cilium-bump: Chart.yaml has no version: $old" >&2; exit 1; }
tar -czf "$unpacked/new.tgz" -C "$unpacked" cilium

cd "$copy"
# the copy is a repository of its own, so tests that read the tracked files run; the rewrite is staged, as a commit would hold it
git init -q && git add -A >/dev/null && git -c user.name=x -c user.email=x@x commit -qm copy
hack/bump-cilium-rewrite.sh "$old" "$new" "$unpacked/new.tgz"
git add -A >/dev/null
go test ./pkg/release/... ./internal/tools/thirdpartynotices/... ./internal/tools/docsciliumversion/... ./pkg/cni/...
