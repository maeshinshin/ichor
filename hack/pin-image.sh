#!/usr/bin/env bash
# pin-image.sh DIGESTS NAME REF: print REF with the digest of image NAME appended (REF:tag@sha256:...).
# DIGESTS is a file of "<repo>@sha256:<hex>" lines, the one the images-push-* make targets write to DIGEST_FILE; the
# last line whose repository ends in /NAME counts. An existing @digest on REF is replaced. A NAME with no digest in
# the file is an error: a release that was asked to pin an image must not quietly fall back to its tag.
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: pin-image.sh DIGESTS NAME REF" >&2; exit 2; }
file=$1 name=$2 ref=$3

digest=$(grep -E "/${name}@sha256:[0-9a-f]{64}\$" "$file" | tail -n 1 | sed 's/.*@//' || true)
[ -n "$digest" ] || { echo "pin-image: no digest for ${name} in ${file}" >&2; exit 1; }

echo "${ref%@*}@${digest}"
