# 0036. `ichor images list` cannot resolve catalogs or schematics, and says so

Status: Superseded by ADR 0090 (the command was removed) · Date: 2026-10-26 · Design section: Monitoring and registry mirrors

Decided: `ichor images list` prints the five fixed Image Factory images (version-gated) plus an unconditional warning naming the two catalogs, instead of trying to resolve a schematic's extensions/overlays into images

## Context

architecture.md's "Monitoring and registry mirrors" section names what `ichor images
list` has to print -- `siderolabs/imager`, `siderolabs/installer-base`
(`siderolabs/installer` below Talos 1.10.0), `siderolabs/extensions`,
`siderolabs/overlays`, `siderolabs/talosctl-all` (1.11.0 and up) -- and
names the trap explicitly: "`extensions` and `overlays` are catalogs ...
every extension and overlay image they reference has to be mirrored too
... A mirror that holds the five images above and nothing else looks
complete and fails at the first schematic that asks for an extension."

The question is whether Ichor can resolve a catalog's or a schematic's
contents into concrete image references. The command must not solve it by
connecting to anything: it never contacts a registry, and a design that
needs to is rejected.

## Research

The `siderolabs/image-factory` repository's `docs/api.md`,
`docs/sources.md`, `docs/air-gapped.md` and `hack/copy-artifacts.sh` show:

- **The schematic itself is opaque to Ichor.** `TalosClusterSchematic`
  (`api/v1alpha1/taloscluster_types.go`) stores only Image Factory's
  schematic ID -- a content hash, not the customization that produced it.
  Recovering what extensions/overlays a schematic names requires
  `GET /schematics/:id`, a call to Image Factory's own HTTP API.
- **There is no endpoint that resolves a schematic ID straight to image
  references.** `GET /schematics/:id` returns the schematic's YAML body
  (extension/overlay *names*); `GET /version/:version/extensions/official`
  and `.../overlays/official` list what each Talos version makes
  available, also by name. Nothing in the documented API turns a name
  into the image reference Ichor would need to print.
- **The catalogs (`siderolabs/extensions`, `siderolabs/overlays`) are
  themselves OCI images, not an HTTP-fetchable manifest.**
  `hack/copy-artifacts.sh` resolves them by pulling the catalog image and
  reading a YAML manifest out of its filesystem; there is no documented
  way to get that mapping without pulling the image.

So resolving "what does this schematic actually need mirrored" needs at
least one network call no matter which path is taken -- Image Factory's
API, or pulling a catalog image -- and this command is offline by design.

## Decision

`buildImageList` (`internal/cli/images_list.go`) only ever returns the
five version-keyed names architecture.md already settled, computed
entirely offline from a Talos version string (`golang.org/x/mod/semver`,
already a direct dependency via `pkg/upgrade`, handles the two version
boundaries -- 1.10.0 for `installer`/`installer-base`, 1.11.0 for
`talosctl-all` -- so this adds no new version-comparison code). `--schematic`
is accepted as a flag (matching the design's "a given version set" of
Talos version plus schematic) but is never read by `buildImageList`: it
exists so a future resolving implementation has a place to land, not as
a currently-functional input. Its doc comment
(`imagesListOptions.Schematic`) records why, pointing here.

Every result carries a fixed `Warning` field (`catalogWarning`) naming
`siderolabs/extensions`/`siderolabs/overlays` as catalogs and stating
plainly that mirroring them does not mirror what they reference --
unconditionally, whether or not `--schematic` was given, since a bare
`--talos-version` call gives no way to know whether some other cluster's
schematic will need an extension this list doesn't name. Silently
printing five images and nothing else is exactly the failure mode this
command exists to prevent (architecture.md's own framing); the command
would rather over-warn on every call than risk an operator reading five
images as a complete mirror.

## Consequences

- An operator whose schematic customizes extensions or overlays still has
  to resolve those manually today (e.g. `hack/copy-artifacts.sh`, which
  already does this by pulling the catalogs) -- `ichor images list` tells
  them this is necessary but does not do it for them.
- If Image Factory's API grows a schematic-resolving endpoint later, or
  running one locally against a pulled catalog becomes acceptable for
  this command (a deliberate scope change, not an oversight), extending
  `buildImageList` to use `imagesListOptions.Schematic` is additive: no
  existing output shape needs to change, only the `Warning` narrowing
  once resolution is possible.
- Not built here: mirror reachability pre-checks (architecture.md calls
  these out as a separate, later feature) and any change to
  `hack/copy-artifacts.sh` itself (not vendored into this repository).
