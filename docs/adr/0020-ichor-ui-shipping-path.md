# 0020. Giving ichor-ui a shipping path

Status: Accepted, amended · Date: 2026-10-25 · Design section: Deployment, Trust

Decided: Gave `ichor-ui` a Dockerfile, a chart Deployment/Service reusing the controller's own mechanism, and RBAC wired into `config/default`

## Context

ADR 0017 (the UI skeleton) and ADR 0018 (the backend's RPCs) both named the
same gap: nothing shipped `ichor-ui` anywhere. `.goreleaser.yaml` built only
`./cmd/ichor`; the chart had no `ichor-ui` `Deployment`/`Service`; and
`config/rbac/ichor-ui/` (ADR 0018) was a standalone kustomize base, wired
into nothing. Two agents reported this independently as a hole. This ADR
closes it: a container image build path, the chart's `Deployment`/`Service`/
`ServiceAccount`, `config/rbac/ichor-ui/` wired into `config/default`, a
kustomize-side `Deployment`/`Service` to match, and a decision on
`.goreleaser.yaml`.

Deploying to a real cluster and verifying it there is explicitly out of
scope: the controller's own image has no path into the inner e2e cluster
yet either, and that needs its own design pass.

## Decision: the container image

`Dockerfile.ichor-ui`, a sibling to the existing `Dockerfile` (which already
has a sibling, `Dockerfile.cross`, so a second top-level Dockerfile is not a
new pattern). Two build stages before the usual distroless one:

- `ui-builder` (`node:24`, matching the version `nix develop` resolves
  `nodejs` to): `npm ci && npm run build`, which Vite already points
  straight at `cmd/ichor-ui/dist` (ADR 0017), so no copy step is needed
  beyond carrying that directory into the Go build stage.
- The Go `builder` stage builds `./cmd/ichor-ui` **with `-tags uiembed`**
  (ADR 0017's own flag) after copying the UI's build output in — the whole
  point of this ADR is to stop shipping the placeholder.

`.dockerignore` re-includes `ui/**` (minus `node_modules`/`dist`, installed
and built fresh inside the image): it previously allowed through only
`*.go` files and `go.mod`/`go.sum`, so the UI's own source tree never
reached the build context at all.

`make docker-build-ichor-ui` mirrors `docker-build` exactly (`$(CONTAINER_TOOL)
build -f Dockerfile.ichor-ui -t ${IMG_UI} .`), with a new `IMG_UI ?=
ichor-ui:latest` beside the existing `IMG`. No push/buildx variants: nobody
asked for them, and they are a three-line addition each if that changes.

## Decision: the chart's Deployment/Service/ServiceAccount

Reuses the controller's own mechanism, not a new one. `dist/chart/
values.yaml` gained an `ichorUI:` block that is `manager:`'s block with the
controller-only bits removed (no `--leader-elect`, no webhook cert volume,
no `POD_NAMESPACE`): same `replicas: 2` default with the same comment
("single-node clusters should set this to 1 and disable
`podDisruptionBudget.enabled`"), the same `podDisruptionBudget.enabled: true / minAvailable: 1`, and the same `topologySpreadConstraints` shape
(`maxSkew: 1`, `topologyKey: kubernetes.io/hostname`, `whenUnsatisfiable:
ScheduleAnyway` — chosen over pod anti-affinity for the same reason
`manager`'s comment already gives: `ScheduleAnyway` degrades to co-located
replicas on a single-node cluster instead of leaving the second one
`Pending` forever). `templates/ui/{ui,ui-pdb,ui-service}.yaml` are
`templates/manager/{manager,manager-pdb}.yaml` and `templates/webhook/
webhook-service.yaml` with the controller-specific pieces removed and a
`tcpSocket` probe in place of the `httpGet` one — `cmd/ichor-ui` has no
HTTP health endpoint (ADR 0017), so a TCP check against the listening port
is the only thing actually true about it today.

Fixed names (`ichor-ui` for the `Deployment`/`ServiceAccount`,
`ichor-ui-service` for the `Service`), not release-templated — matching
`ichor-webhook-service`'s own precedent (one installation per cluster,
design 4.1) and `config/rbac/ichor-ui/`'s existing fixed names (ADR 0018), so
the two sides agree without either needing to know the other's Helm release
name. `ichor ui`'s future port-forward (out of scope here, another agent's
`internal/cli` work) needs exactly this: a name it can find without asking
Helm what the release was called.

No ClusterIP override: architecture.md "Trust" puts the UI behind `ichor ui`
(a port-forward) in the first release, not an exposed Service, so the
default (`ClusterIP`) is correct as-is and needed no decision.

## Decision: `config/rbac/ichor-ui/` wired into `config/default`, and a matching kustomize Deployment/Service

`config/default/kustomization.yaml` gained `../rbac/ichor-ui` as its own
resource entry, a sibling to `../rbac`, not folded into it — the task's own
constraint ("must not mix with the controller's Role") matches
architecture.md "Trust" directly: the two identities must never share a
kustomization any more than they share a `ServiceAccount`.

Wiring RBAC in without a workload for it to apply to was exactly what ADR
0018 flagged as the reason *not* to wire it in yet. So this ADR also adds
`config/manager-ui/` (`manager.yaml`, `service.yaml`, `kustomization.yaml`),
a sibling to `config/manager/` — and, matching `config/manager/`'s own
simplicity, **not** the chart's richer treatment: plain `replicas: 1`, no
`PodDisruptionBudget`, no `topologySpreadConstraints`. The 2-replica/PDB/
anti-affinity shape is a Helm-chart-only enrichment for the controller
today (confirmed: nothing under `config/` has a `PodDisruptionBudget` or
`topologySpreadConstraints` anywhere), so giving `ichor-ui`'s kustomize base
that treatment while the controller's own kustomize base doesn't have it
would be a new, one-off inconsistency, not reuse.

`make deploy` and `make build-installer` now also run `cd config/manager-ui
&& kustomize edit set image ichor-ui=${IMG_UI}`, mirroring the existing
`controller=${IMG}` line exactly.

**Found and fixed in the same pass**: `config/default`'s two JSON6902
patches (`manager_metrics_patch.yaml`, `manager_webhook_patch.yaml`) target
`kind: Deployment` with no `name`, which silently relied on there being
exactly one `Deployment` in the whole kustomization. Adding `ichor-ui`'s
broke that assumption immediately — `kustomize build config/default` failed
outright (`add operation does not apply: ... missing value`), trying to
patch `ichor-ui`'s `volumeMounts`/`args`, which it doesn't have. Both
patches' `target` now also carries `name: controller-manager`. Verified:
`kustomize build config/default` succeeds, the controller's `Deployment`
still carries its webhook volume/mount/port and metrics arg, and `ichor-ui`'s
is untouched by either patch.

## Resolved gap: the chart's ichor-ui secrets Role no longer diverges from `config/rbac/ichor-ui/role.yaml`

`internal/uiapi/rbac_markers.go`'s secrets marker originally carried no
`namespace=` qualifier (`+kubebuilder:rbac:groups="",resources=secrets,
verbs=get`), unlike the controller's three equivalent markers
(`cmd/main.go:66-67`, `internal/controller/talosmachine_controller.go:158`,
all `namespace=ichor-system`). Controller-gen folded that rule into
`ichor-ui-role`'s cluster-scoped `ClusterRole`, where the controller's
equivalent rule is split into a separate namespaced `Role`
(`manager-secrets-role.yaml`) specifically so it isn't cluster-wide.
`dist/chart/chart_test.go`'s `checkNoClusterWideSecretsRead` — written
earlier to guard a real regression in the controller's own role, and
generic over every `ClusterRole` the chart renders, not controller-specific
— correctly flagged this as the same pattern: an unrestricted `secrets: get`
with no `resourceNames`, readable as literally any Secret in the cluster if
its name is known.

At the time this ADR was first written, fixing the marker was out of that
change's permitted paths, so the chart's RBAC was made to intentionally
diverge from `config/rbac/ichor-ui/role.yaml`: `templates/rbac/
ichor-ui-role.yaml`'s `ClusterRole` dropped the secrets rule, and `templates/
rbac/ichor-ui-secrets-{role,rolebinding}.yaml` carried it as a namespace-
scoped `Role`, mirroring `manager-secrets-role.yaml`/
`manager-secrets-rolebinding.yaml` exactly. That was narrower than what
`config/rbac/ichor-ui/` shipped via kustomize at the time, never wider, so
the divergence was safe in the direction it went.

**The marker has since gained `namespace=ichor-system`** on that one line.
`make manifests-ichor-ui` now generates `config/rbac/ichor-ui/role.yaml` as
the same two-object shape `config/rbac/role.yaml` already has for the
controller: a cluster-scoped `ClusterRole` (`talosclusters`,
`talosconfigpatches`, `talosmachines` — no secrets) and a namespaced `Role`
named `ichor-ui-role` in `ichor-system` (`secrets: get`, nothing else). A
new `config/rbac/ichor-ui/secrets_role_binding.yaml` binds that `Role` to
the `ichor-ui` `ServiceAccount` (`config/rbac/ichor-ui/kustomization.yaml`
now lists it), the same way `config/rbac/manager_secrets_role_binding.yaml`
already does for the controller. The chart's own `ichor-ui-secrets-
{role,rolebinding}.yaml` templates are **not deleted or folded into
`ichor-ui-role.yaml`**: they already matched the controller's own chart
pattern (`manager-role.yaml` and `manager-secrets-role.yaml` are separate
files too, not one multi-document file), so there was nothing left to
converge once the generated kustomize output reached the same shape. The
two deployment paths (chart, kustomize) now agree structurally: a
`ClusterRole` without secrets, plus a namespace-scoped `Role`/`RoleBinding`
pair that grants only `get` on `secrets` in `ichor-system`, in both.

## Decision: `.goreleaser.yaml` stays as-is

Not extended to build or archive `ichor-ui`. `ichor` (the CLI) is a binary a
user runs on their own workstation — exactly what `goreleaser`'s
`builds`/`archives` sections exist to produce and distribute as a
downloadable `tar.gz`. `ichor-ui` is never run outside a container; its only
real distribution artifact is the image `Dockerfile.ichor-ui` builds, consumed
by the chart or `config/manager-ui/`. A `ichor-ui_<version>_linux_amd64.tar.gz`
would have no use anyone has: nothing downloads it and runs it bare, since
it needs RBAC, a `ServiceAccount` and a cluster to do anything at all.
`goreleaser` also has no `dockers:` section in this repo today, and nothing
else uses one — adding `ichor-ui`'s image there instead of the Makefile
target this ADR already built would be a second way to do the same thing.

## Consequences

- `ichor-ui` has a real build path end to end: `make docker-build-ichor-ui`
  produces an image with the actual UI embedded, the chart and
  `config/manager-ui/` both deploy it with its own identity, separate from
  the controller's throughout.
- `dist/chart/chart_test.go` gained `checkUIBackendRBACIsSeparate`, guarding
  architecture.md "Trust" the same way `checkNoClusterWideSecretsRead`
  guards ADR 0011: the UI's `Deployment` must run as its own
  `ServiceAccount`, bound only to its own `ClusterRole`, with at least one
  other binding (the controller's) naming neither. Verified by deliberately
  breaking each half (pointing the `Deployment` at the controller's
  `ServiceAccount`; pointing the `ClusterRoleBinding` at a different
  `ClusterRole` name) and confirming the test fails with a message naming
  the mismatch, then reverting.
- Real-cluster deployment and verification remain open, same as the
  controller's own image today — not a new gap this ADR introduces.
- The secrets-rule divergence above is resolved: `rbac_markers.go`'s one-
  line fix (`namespace=ichor-system`) landed, `config/rbac/ichor-ui/` and
  the chart now grant the identical, namespace-scoped `secrets: get`, and
  `checkNoClusterWideSecretsRead` passes for `ichor-ui-role` the same way
  it already did for `manager-role` — confirmed by deliberately
  reintroducing an unrestricted `secrets: get` into `ichor-ui-role.yaml`'s
  `ClusterRole` and watching the test fail naming it, then reverting.
