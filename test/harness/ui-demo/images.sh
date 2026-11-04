#!/usr/bin/env bash
# Build the three demo images from this tree with crane (no container runtime) and push them to
# $REGISTRY. Run through `make demo-images`, which builds the UI and dist/install.yaml first.
# The git tree is left unchanged: build-installer works on a temp copy of config/.
#
# This is the one path that builds released images (ADR 0108); the Dockerfiles are for local `docker build`.
# The release workflow runs it in halves so the job that fetches dependencies holds no registry token:
#   images.sh build   compile the three binaries into bin/img (no registry needed)
#   images.sh push    layer the binaries on the pinned base and push (needs only crane)
#   images.sh         both
# build and push, with the install manifest naming the controller and ichor-ui images by tag.
#
# The installer embeds that manifest, so a signed installer would still deploy whatever the tags point at. The
# digest flow (ADR 0122, follow-up) builds the installer last, from a manifest that names the other two by digest:
#   images.sh build-core        compile the controller and ichor-ui binaries
#   images.sh push-core         push them; DIGEST_FILE receives their digests
#   (make build-installer IMAGE_DIGESTS=$DIGEST_FILE renders the manifest with them)
#   images.sh build-installer   check the manifest names those digests (IMAGE_DIGESTS), embed it, compile ichor
#   images.sh push-installer    push the installer; its digest is appended to DIGEST_FILE
# DIGEST_FILE, when set, receives one "<repo>@<digest>" line per pushed image for signing and attestation.
set -euo pipefail

MODE=${1:-all}
case "$MODE" in all | build | push | build-core | push-core | build-installer | push-installer) ;;
  *) echo "usage: images.sh [build|push|build-core|push-core|build-installer|push-installer]" >&2; exit 2 ;;
esac

: "${VERSION:?set VERSION, e.g. v0.0.8-\$(git rev-parse --short HEAD)}"
case "$MODE" in
  build | build-core | build-installer) ;;
  *) : "${REGISTRY:?set REGISTRY, e.g. localhost:5000 (a kubectl port-forward to svc/<prefix>-registry)}" ;;
esac
[ "$VERSION" != dev ] || { echo "demo-images: VERSION must not be 'dev'" >&2; exit 1; }

# Pinned by digest (a moving tag would change a release silently); Renovate bumps it. BASE_IMAGE is for tests.
BASE=${BASE_IMAGE:-gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329}
NAME=github.com/maeshinshin/ichor/internal/version.Version
LIVE=ghcr.io/maeshinshin # the names the install manifest carries; nodes mirror them to $REGISTRY
PUSH=${REGISTRY:-}/maeshinshin
INSECURE=${CRANE_INSECURE---insecure} # a local plain-HTTP registry by default; the release workflow sets it empty for ghcr.io

work=$(mktemp -d)
trap 'rc=$?; [ $rc -eq 0 ] || cat "$work/crane.log" >&2; rm -rf "$work"' EXIT
: > "$work/crane.log"
here=$(cd "$(dirname "$0")" && pwd)

# embed_manifest: take dist/install.yaml (built by `make build-installer` from a temp copy of config/ with the LIVE
# names) into the tree the installer is compiled from. With IMAGE_DIGESTS (the digest flow) it must name the
# controller and ichor-ui images by those digests: an installer that carried tags would defeat the signature check.
embed_manifest() {
  grep -q "$LIVE/ichor-controller:$VERSION" dist/install.yaml || { echo "demo-images: install manifest lacks the controller image" >&2; exit 1; }
  grep -q "$LIVE/ichor-ui:$VERSION" dist/install.yaml || { echo "demo-images: install manifest lacks the UI image" >&2; exit 1; }

  if [ -n "${IMAGE_DIGESTS:-}" ]; then
    local n pinned
    for n in ichor-controller ichor-ui; do
      pinned=$("$here/../../../hack/pin-image.sh" "$IMAGE_DIGESTS" "$n" "$LIVE/$n:$VERSION")
      grep -qF -- "$pinned" dist/install.yaml || { echo "demo-images: install manifest does not name $pinned (render it with make build-installer IMAGE_DIGESTS=$IMAGE_DIGESTS)" >&2; exit 1; }
    done
  fi

  mkdir -p internal/installmanifest
  cp dist/install.yaml internal/installmanifest/install.yaml
  cp dist/network-policies.yaml internal/installmanifest/network-policies.yaml
}

touch "$work/start"
digests=${DIGEST_FILE:-/dev/null}

# build OUT PKG TAGS [no]: a fresh static binary, newer than the start of this run and stamped with VERSION
# (ichor-ui links no version package, so it passes "no" and is only checked for freshness).
build() {
  local out=$1 pkg=$2 tags=$3 stamped=${4:-yes}
  mkdir -p "$(dirname "$out")"
  rm -f "$out"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ${tags:+-tags "$tags"} -trimpath -ldflags "-s -w -X $NAME=$VERSION" -o "$out" "$pkg"
  [ "$out" -nt "$work/start" ] || { echo "demo-images: $out is not newer than this build's start" >&2; exit 1; }
  [ "$stamped" = no ] || grep -aq -- "$VERSION" "$out" || { echo "demo-images: $out does not contain $VERSION" >&2; exit 1; }
}

# ship BIN REPO: append BIN as a layer on the distroless base, set the entrypoint, push, verify, print the digest.
ship() {
  local bin=$1 repo=$2 ref="$PUSH/$2:$VERSION" layer
  layer="$work/$repo.tar"
  tar -C "$(dirname "$bin")" --owner=0 --group=0 --numeric-owner --mtime=@0 --sort=name --mode=755 -cf "$layer" "$(basename "$bin")"
  crane append $INSECURE --set-base-image-annotations -b "$BASE" -f "$layer" -t "$ref" 2>>"$work/crane.log" > /dev/null
  crane mutate $INSECURE --entrypoint "/$(basename "$bin")" -t "$ref" "$ref" 2>>"$work/crane.log" > /dev/null
  crane config $INSECURE "$ref" | grep -q "sha256:$(sha256sum "$layer" | cut -d' ' -f1)" \
    || { echo "demo-images: $ref does not carry the layer just built" >&2; exit 1; }
  local digest
  digest=$(crane digest $INSECURE "$ref")
  printf '%-45s %s\n' "$ref" "$digest"
  echo "$PUSH/$repo@$digest" >> "$digests"
}

build_core() {
  build bin/img/manager/manager ./cmd ""
  build bin/img/ichor-ui/ichor-ui ./cmd/ichor-ui uiembed no
}

build_installer() {
  embed_manifest
  build bin/img/ichor/ichor ./cmd/ichor installembed
}

# The digest file is written by the first push of a flow and appended to by push-installer.
start_digests() { [ "$digests" = /dev/null ] || : > "$digests"; }

push_core() {
  start_digests
  echo "pushed to $REGISTRY:"
  ship bin/img/manager/manager ichor-controller
  ship bin/img/ichor-ui/ichor-ui ichor-ui
}

push_installer() {
  echo "pushed to $REGISTRY:"
  ship bin/img/ichor/ichor ichor-installer
}

case "$MODE" in
  build) build_installer; build_core ;;
  build-core) build_core ;;
  build-installer) build_installer ;;
  push) push_core; push_installer ;;
  push-core) push_core ;;
  push-installer) push_installer ;;
  all) build_installer; build_core; push_core; push_installer ;;
esac
