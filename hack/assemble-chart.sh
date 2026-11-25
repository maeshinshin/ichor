#!/usr/bin/env bash
# assemble-chart.sh CHART_DIR DEST: copy the chart to DEST/ichor with the files a published chart carries
# (LICENSE, NOTICE, THIRD-PARTY-NOTICES) and, when IMAGE_DIGESTS is set, the controller and ichor-ui images pinned by
# digest (the templates add no tag to a repository with an @). make helm-package packages the result and make
# check-release-record measures it, so the gate sees what is shipped.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: assemble-chart.sh CHART_DIR DEST" >&2; exit 2; }
src=$1 dest=$2/ichor
cp -r "$src" "$dest"
cp LICENSE NOTICE THIRD-PARTY-NOTICES "$dest/"
[ -z "${IMAGE_DIGESTS:-}" ] && exit 0
for n in ichor-controller ichor-ui; do
  pin=$(hack/pin-image.sh "$IMAGE_DIGESTS" "$n" "ghcr.io/maeshinshin/$n:${APP_VERSION}")
  sed -i "s|^\( *repository: \)ghcr.io/maeshinshin/$n\$|\1$pin|" "$dest/values.yaml"
  grep -qF "$pin" "$dest/values.yaml" || { echo "assemble-chart: the chart has no default $n image to pin" >&2; exit 1; }
done
