#!/usr/bin/env bash
# Build the three demo images from this tree with crane (no container runtime) and push them to
# $REGISTRY. Run through `make demo-images`, which builds the UI and dist/install.yaml first.
# The git tree is left unchanged: build-installer works on a temp copy of config/.
set -euo pipefail

: "${VERSION:?set VERSION, e.g. v0.0.8-\$(git rev-parse --short HEAD)}"
: "${REGISTRY:?set REGISTRY, e.g. localhost:5000 (a kubectl port-forward to svc/<prefix>-registry)}"
[ "$VERSION" != dev ] || { echo "demo-images: VERSION must not be 'dev'" >&2; exit 1; }

BASE=gcr.io/distroless/static:nonroot
NAME=github.com/maeshinshin/ichor/internal/version.Version
LIVE=ghcr.io/maeshinshin # the names the install manifest carries; nodes mirror them to $REGISTRY
PUSH=$REGISTRY/maeshinshin

work=$(mktemp -d)
trap 'rc=$?; [ $rc -eq 0 ] || cat "$work/crane.log" >&2; rm -rf "$work"' EXIT
: > "$work/crane.log"

# dist/install.yaml was built by `make build-installer` (from a temp copy of config/) with the LIVE names.
grep -q "$LIVE/ichor-controller:$VERSION" dist/install.yaml || { echo "demo-images: install manifest lacks the controller image" >&2; exit 1; }
grep -q "$LIVE/ichor-ui:$VERSION" dist/install.yaml || { echo "demo-images: install manifest lacks the UI image" >&2; exit 1; }
cp dist/install.yaml internal/installmanifest/install.yaml

touch "$work/start"

# build OUT PKG TAGS [no]: a fresh static binary, newer than the start of this run and stamped with VERSION
# (ichor-ui links no version package, so it passes "no" and is only checked for freshness).
build() {
  local out=$1 pkg=$2 tags=$3 stamped=${4:-yes}
  mkdir -p "$(dirname "$out")"
  rm -f "$out"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ${tags:+-tags "$tags"} -ldflags "-X $NAME=$VERSION" -o "$out" "$pkg"
  [ "$out" -nt "$work/start" ] || { echo "demo-images: $out is not newer than this build's start" >&2; exit 1; }
  [ "$stamped" = no ] || grep -aq -- "$VERSION" "$out" || { echo "demo-images: $out does not contain $VERSION" >&2; exit 1; }
}

# ship BIN REPO: append BIN as a layer on the distroless base, set the entrypoint, push, verify, print the digest.
ship() {
  local bin=$1 repo=$2 ref="$PUSH/$2:$VERSION" layer
  layer="$work/$repo.tar"
  tar -C "$(dirname "$bin")" --owner=0 --group=0 --mode=755 -cf "$layer" "$(basename "$bin")"
  crane append --insecure -b "$BASE" -f "$layer" -t "$ref" 2>>"$work/crane.log" > /dev/null
  crane mutate --insecure --entrypoint "/$(basename "$bin")" -t "$ref" "$ref" 2>>"$work/crane.log" > /dev/null
  crane config --insecure "$ref" | grep -q "sha256:$(sha256sum "$layer" | cut -d' ' -f1)" \
    || { echo "demo-images: $ref does not carry the layer just built" >&2; exit 1; }
  printf '%-45s %s\n' "$ref" "$(crane digest --insecure "$ref")"
}

build bin/img/manager/manager cmd/main.go ""
build bin/img/ichor-ui/ichor-ui ./cmd/ichor-ui uiembed no
build bin/img/ichor/ichor ./cmd/ichor installembed

echo "pushed to $REGISTRY:"
ship bin/img/manager/manager ichor-controller
ship bin/img/ichor-ui/ichor-ui ichor-ui
ship bin/img/ichor/ichor ichor-installer
