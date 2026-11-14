# 0090. Air-gapped installs are out of scope

Status: Accepted · Date: 2026-10-27 · Design section: Non-goals, Monitoring and registry mirrors

Decided: air-gapped environments are no longer a requirement, so `ichor images list` (with `--kokumi`) and the design's air-gap section and phase-10 "air-gap verification" are removed, while registry mirrors, registry auth, `ichor init --registry-mirror`, the installer-repository override and the self-hosted Image Factory URL stay because pull-through caches and private registries need them

## Context

The design carried air-gap support as an optional feature: mirrors, an installer image override, a self-hosted Image
Factory and `ichor images list` to print what had to be mirrored. Air-gapped
environments are not a supported target. Features that exist only for it are cost without a user.

## Decision

Removed:

- `ichor images list` and the `ichor images` group, including `--kokumi`, `--image-registry` rewriting and the
  Image Factory catalog warning (ADR 0036); `kokumiImages` and its tests.
- The design's "Air gap" paragraph under Kokumi, the `ichor images list` paragraphs of the air-gap section (the
  section is now "Monitoring and registry mirrors"), `ichor images list` from phase 9 and "air-gap verification"
  from phase 10. Air-gapped environments are listed under Non-goals.
- Air-gap wording in API doc comments, the chart and code comments.

Kept, with the reason:

- `TalosCluster.spec.registries` (mirrors and pull credentials, ADR 0027) and `ichor init --registry-mirror`:
  pull-through caches and private registries, used by the demo.
- `IchorInstallation.spec.installerRepository`, the installer image override and the self-hosted Image Factory URL:
  usable with any private registry or Factory.
- The installer image's embedded manifest (ADR 0019, ADR 0059): it keeps `ichor init` and upgrades free of a fetch
  step and version drift, which is independent of air gaps.
- The pre-check for mirror reachability.

Nothing was removed from the proto API, so no field numbers are reserved.

## Consequences

- Ichor builds nothing for an installation with no route to public registries; a user who mirrors images does
  so by hand.
- ADR 0036 is superseded; ADR 0048 (`ichor images list --kokumi` only) and ADR 0059 (its air-gap rationale and the
  `ichor images list` pointer) are superseded in part. Older ADRs that mention air gap as a motive are left as
  history.
