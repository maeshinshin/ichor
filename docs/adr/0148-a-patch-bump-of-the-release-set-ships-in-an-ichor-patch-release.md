# 0148. A patch bump of the release set ships in an Ichor patch release

Status: Accepted · Date: 2026-11-11 · Design section: Upgrades · Touches: 0113, 0115, 0119, 0142

Decided: a change to the pinned Talos, Kubernetes or Cilium PATCH version ships in an Ichor PATCH release (v0.x.y while Ichor is pre-1.0, vX.Y.Z after), and a change to their MINOR or MAJOR version ships in an Ichor MINOR release, because it changes the behaviour a fresh install gets. The pinned set is the fresh-install default only (ADR 0115); bumping it moves no cluster Ichor already manages, which is why a patch bump is safe in a patch release. A patch bump changes no CRD field and no API shape; if it would, the release is a minor one. A security fix in one of the three is the usual case and does not wait for the next minor. Patch releases are cut from the branch `release-X.Y`, to which the change is cherry-picked from `main`.

## Context

ADR 0113 made each Ichor build pin one set of Talos, Kubernetes and Cilium versions, and ADR 0115 narrowed it: the set decides a first install and nothing else, and each component upgrades on its own afterwards. ADR 0119 automated the Cilium patch bump and said a merged bump reaches users with the next Ichor release, without saying what kind of release that is. Nothing says when a bump may go out as a patch release and when it needs a minor one, so each release would decide it again.

## Decisions

1. **The rule.** Compare the new set with the one in the last release. If only patch versions moved (Talos v1.14.5 to v1.14.6, Kubernetes v1.36.6 to v1.36.7, Cilium 1.20.4 to 1.20.5), the next release may be an Ichor patch release. If any component's minor or major moved, including the Talos version contract (`v1.14` to `v1.15`), the next release is an Ichor minor release. Pre-1.0 a minor release is v0.X.0 and a patch release is v0.X.Y; from 1.0 they are vX.Y.0 and vX.Y.Z. A major of the set (a Kubernetes 2.0) is still an Ichor minor: Ichor's own major is for a breaking change to Ichor's own API and not for the set.
2. **Why a patch bump is safe in a patch release.** The set is what `ichor init`, the CLI defaults and the UI's proposed values read (ADR 0115). A cluster that exists is upgraded per component, by the user, through the upgrade machinery; for Cilium the cluster only moves to the chart of the Ichor that runs it, and only on `ichor upgrade cni` (ADR 0119). Installing a patch release of Ichor therefore changes what the next new cluster gets, and the chart `ichor upgrade cni` offers, and moves nothing that is running.
3. **A patch bump is not an API change.** A patch bump of the set changes no CRD field, no CRD schema and no proto message. If moving the Talos machinery module (`go.mod`) makes `make generate` or `make manifests` change `api/`, `config/crd/bases`, `dist/chart/templates/crd` or `proto/`, the release is not a patch release, whatever the version numbers say, and it ships as a minor.
4. **Security fixes ship at once.** A fix in Talos, Kubernetes or Cilium that lands as a patch is cut as an Ichor patch release when its bump is merged. It does not wait for work queued for the next minor. ADR 0119's daily Cilium bump opens the pull request for Cilium; Talos and Kubernetes are moved by a person in the places listed below. Either way a person reads the upstream notes before merging.
5. **The changelog says what the set is.** A release whose set changed has an "Install defaults" section, in the form of v0.1.0's: the table of the three versions a first install gets, and for a patch release one sentence that says which moved ("Talos v1.14.5 to v1.14.6; nothing running moves"). A release whose set did not change leaves the section out, as v0.2.0 does.

## Release branches

- `main` is the next minor release. All feature work lands on `main`.
- `release-X.Y` is the line for that minor's patch releases (the branch ADR 0142 builds the docs line from). A change that belongs in a patch release lands on `main` first, is cherry-picked to `release-X.Y`, and the patch tag is cut there.
- The split happens when the first change that belongs only to the next minor is ready to land. Until then `main` and the current minor's line are the same thing, and `release-X.Y` is fast-forwarded to the tagged point.
- A patch bump of the release set is exactly the kind of change that is cherry-picked to `release-X.Y`.
- A cherry-pick has a different commit id on the release line than on `main`. That is expected.

## Where the set lives

- `pkg/release/release.go`, `Pinned`: `Talos`, `VersionContract`, `Kubernetes`, and `Cilium`, which reads `cilium.ChartVersion`.
- `pkg/cni/cilium/values.go`: `ChartVersion`, and `RenderKubernetesVersion`, which must equal `Pinned.Kubernetes`. A Cilium bump also moves the Makefile pins `CILIUM_CHART_VERSION` and `CILIUM_CHART_SHA256`, the chart archive `pkg/cni/cilium/chart/cilium-X.tgz` with its `go:embed` line, and the bootstrap goldens. `hack/bump-cilium-patch.sh` moves them all for a patch (ADR 0119); a Cilium minor is moved by hand.
- `go.mod`: `github.com/siderolabs/talos/pkg/machinery` must agree with `Pinned.Talos`, and `Pinned.Kubernetes` must be one the pinned Talos supports (`pkg/release/release_test.go` checks both).
- `pkg/release/compat.go`: the Talos-to-Kubernetes and Cilium-to-Kubernetes ranges. They change only when a minor does.
- `config/samples/ichor_v1alpha1_taloscluster.yaml` and the version tables in the docs name the set and follow it.

`internal/releasecheck/releasecheck.go`, `internal/controller/release_check.go` and `internal/controller/talos_egress.go` do not hold the set. The first two list the newest upstream releases an upgrade can target (their floors are in `pkg/release/targets.go`), and the third builds the controller's egress policy for the Talos nodes. A bump does not touch them.

## Rejected

- A minor release for every bump of the set. A security patch would then wait for the next minor, and the bump changes nothing for a running cluster.
- A patch release for every bump of the set. A Kubernetes or Cilium minor changes the values and the options a fresh install gets (ADR 0119 never crosses a minor for this reason), so it is a minor release.
- Counting the set in Ichor's major version. The set is not Ichor's API.

## Consequences

The Cilium workflow's pull request and a patch bump of Talos or Kubernetes are followed by a patch release without a further decision. A bump that crosses a minor is a person's change and ships in a minor release, as ADR 0119 already has it. A release that carries a patch bump and a CRD change is a minor release.
