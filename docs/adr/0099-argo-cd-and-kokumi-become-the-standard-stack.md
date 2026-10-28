# 0099. Argo CD and Kokumi become Ichor's standard stack; Cilium is the only CNI and Ichor's own Helm release

Status: Proposed · Date: 2026-10-29 · Design section: Bootstrap, GitOps and Argo CD, CNI · Supersedes in part: 0013, 0019, 0024, 0028, 0048, 0055

Decided: `ichor init` always installs Cilium, Kokumi and Argo CD with no opt-out; Cilium is the only CNI, bootstrapped through Talos inline manifests and then owned by Ichor as a Helm release run by an installer-style Job with user values stored on the TalosCluster; Kokumi and Argo CD are applied once, then managed by Argo CD from artifacts the Ichor controller publishes into Kokumi's in-cluster registry, so no Git repository is needed

## Context

Four things in the repo no longer fit what the user now wants.

1. Argo CD and Kokumi are optional and Ichor installs neither (ADR 0019, 0048). `--argocd`, `--kokumi`, `--repo`, `--gitops-dir` and `gitops export` exist to serve that option.
2. The CNI is a choice (`flannel`, `cilium`, `custom`, ADR 0013), and Cilium is handed to Argo CD after bootstrap (ADR 0024, 0055). Users now need to edit Cilium's Helm values from the Ichor UI, which an Argo CD-owned release in a Git repository cannot offer.
3. Kokumi is built only as a repo-creds Secret (ADR 0048). Its install, its storage and its real data model were never checked against a running release.
4. `docs/design/architecture.md` says Ichor "bootstraps CNI and Argo CD once and hands them over". That sentence stays true for Argo CD and Kokumi. For Cilium it is replaced here.

User decisions this ADR implements (2026-10-29): Argo CD and Kokumi are the standard stack, always installed; Argo CD syncs from Kokumi's in-cluster OCI registry; Cilium is the only CNI and existing flannel/custom clusters may be deleted; Argo CD manages Cilium's neighbours (itself and Kokumi) after a one-time bootstrap, while Cilium stays Ichor-managed and UI-editable; Ichor publishes artifacts into Kokumi so a later "addons" UI can edit component values; tracking detection of Ichor's own CRs by a user's GitOps stays; Cilium-aware NetworkPolicies for the Kokumi and Argo CD namespaces are applied after init.

## Research

All versions below were read on 2026-10-29. The Kokumi release was downloaded and its checksum matched ADR 0048 (`install.yaml` sha256 `345223726800ca3f08f633c76c91a38265bc0c131551b24dc49c69ee0cd7b840`). Its source tarball was read for the docs that ship in `docs/content` (the kokumi.dev pages are older than release 0.17.0: they describe `Product`, `Registry` and `Repository` kinds that 0.17.0 does not contain; the source tree is authoritative).

### Kokumi

- Latest stable: **0.17.0** (2026-10-11), https://github.com/kokumi-dev/kokumi/releases. Apache-2.0, "experimental", API `delivery.kokumi.dev/v1alpha1`. Context7 has no Kokumi entry (the only hit was an unrelated library), so kokumi.dev and the source were used.
- Install: only `kubectl apply -f .../releases/download/0.17.0/install.yaml` (https://kokumi.dev/docs/installation/). There is no Helm chart and no OCI install artifact. The file is 134,389 bytes (14.9 KB gzipped). It holds one Namespace (`kokumi`), 8 CRDs (Approval, Kitchen, Menu, Order, Pantry, Preparation, Recipe, Serving), about 40 RBAC objects, 3 Deployments, 3 Services, 1 PVC, 1 ConfigMap, 1 Secret and 2 ValidatingAdmissionPolicies. Requires Kubernetes >= 1.26 and Argo CD >= 3.3.
- Images (exactly two): `ghcr.io/kokumi-dev/kokumi:0.17.0` (controller manager and API server/UI) and `registry:3.1.2@sha256:ddf75434...` (bundled registry). No scanner is in 0.17.0.
- Workloads and resources set in the manifest: controller manager (requests 10m/64Mi, limit 500m/128Mi), server (same), registry (request 100m/256Mi, limit 256Mi). Sum of requests 120m/384Mi, sum of limits 1.1 CPU/512Mi.
- Registry storage: PVC `kokumi-registry-pvc`, 1Gi, ReadWriteOnce, **no storageClassName**. A Talos cluster has no StorageClass unless the user adds one (Ichor installs no storage), so the registry pod would stay Pending. The registry Service is `type: NodePort` (30050) and the registry accepts anonymous reads and writes over HTTP; the server Secret `kokumi-server-auth` ships `admin`/`admin`. All three need handling (decisions 6 and 7).
- Data model (source `docs/content/docs/architecture.md`, `getting-started.md` at 0.17.0): an **Order** takes a source OCI artifact, renders it, and pushes the result as an immutable **Preparation** to a destination registry (default in-cluster, `oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/<name>`). A **Serving** (one per Order, created automatically) points at one Preparation and creates the Argo CD Application. `promotion.mode` is `Automatic` (newest Ready Preparation) or `Manual`. A **Menu** is a template with override policies; a **Pantry** carries registry URL and credentials; a **Recipe** is a render profile.
- Source formats: with no `spec.render`, the source artifact must hold `*.yaml` files at its root, stored as-is ("pre-rendered manifest bundle"). With `spec.render.helm` it must be a Helm chart. In code (`internal/oci/oras_client.go`) the accepted single-layer media types are `application/vnd.cncf.helm.chart.content.v1.tar+gzip` and `application/vnd.cncf.flux.content.v1.tar+gzip` (a gzipped tar of the files).
- Publishing: there is no Kokumi API to publish a source. Ichor pushes an OCI artifact to the registry itself (oras-go or go-containerregistry are both fine; the registry is the CNCF `registry` image, plain HTTP, no auth) and creates an Order whose `spec.source.oci` and `version` name it. Ichor never needs a Product/Menu.
- The Application Kokumi creates (`internal/deployer/argocd.go`): name = Order name, namespace `argocd`, `project: default`, `source {repoURL: oci://<registry>/preparation/<name>, targetRevision: <digest>, path: "."}`, `destination {server: https://kubernetes.default.svc, namespace: <Order namespace>}`, `syncPolicy.automated {prune, selfHeal}` and `ServerSideApply=true`, annotation `delivery.kokumi.dev/allowed-order`. It refuses to update a pre-existing Application without that annotation. Consequence: an Order in namespace `X` named `N` produces Application `N` that deploys into namespace `X`; Ichor creates the Order `argocd` in namespace `argocd` and the Order `kokumi` in namespace `kokumi`.
- Argo CD reads that artifact as an OCI source: one layer, media type `application/vnd.oci.image.layer.v1.tar+gzip` or the Helm chart type by default, `path` selects inside the image (https://argo-cd.readthedocs.io/en/stable/user-guide/oci/). The docs there do not mention `insecureOCIForceHttp`; Kokumi's own Getting Started does, via a repo-creds Secret (`type: oci`, `url: oci://kokumi-registry.kokumi.svc.cluster.local:5000`, `insecureOCIForceHttp: "true"`), which ADR 0048 already builds.

### Argo CD

- Latest stable 3.x: **v3.5.6** (2026-10-20). Also current: v3.4.12, v3.3.18; v3.6.0-rc4 is a prerelease. Kokumi 0.17.0 requires >= 3.3 and its own Getting Started installs v3.5.1; ADR 0019 and 0055 verified shapes against v3.5.x (ADR 0055's e2e ran v3.5.6). Pin **3.5.6**.
- Install forms: `manifests/install.yaml` (1,917,766 bytes, 106 KB gzipped; images `quay.io/argoproj/argocd:v3.5.6`, `redis:8.2.3-alpine`, `dex:v2.45.1`; no resource requests set) or `core-install.yaml` (no API server/UI/dex, and no `default` AppProject, which made ADR 0055's e2e fail with `InvalidSpecError` until one was created; Kokumi's Applications use `project: default`). Or the Helm chart `argo-cd` **10.11.6** (appVersion v3.5.6, https://github.com/argoproj/argo-helm, 237,904 bytes, `kubeVersion >=1.25`, dependency `redis-ha` that is off by default). Pinned manifests give no switches for dex, notifications and the ApplicationSet controller; the chart does.
- Self-management: "Argo CD is able to manage itself since all settings are represented by Kubernetes manifests", and it must use `ServerSideApply=true` (https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/). Kokumi's Applications already set it.
- Not answered by the docs read: a size limit for an OCI source, the version in which OCI sources left beta, and whether `insecureOCIForceHttp` has a documented `argocd-cm` home. All three are checked in the e2e (work unit 14).

### Talos and Cilium (https://docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium, Talos v1.14 docs)

Prescribed and already matched by `pkg/cni/cilium/values.go`: `ipam.mode=kubernetes`; `kubeProxyReplacement=false|true`; `securityContext.capabilities.ciliumAgent` (CHOWN, KILL, NET_ADMIN, NET_RAW, IPC_LOCK, SYS_ADMIN, SYS_RESOURCE, DAC_OVERRIDE, FOWNER, SETGID, SETUID; no SYS_MODULE) and `cleanCiliumState` (NET_ADMIN, SYS_ADMIN, SYS_RESOURCE); `cgroup.autoMount.enabled=false`, `cgroup.hostRoot=/sys/fs/cgroup`; `k8sServiceHost=localhost`, `k8sServicePort=7445` for the kube-proxy-free case; KubePrism enabled on 7445.

Where it matters for this ADR:

- "Talos only creates missing resources from inline manifests -- it never deletes or updates them." To change them, "edit all control plane machine configurations and run `talosctl upgrade-k8s`." So an inline manifest cannot carry a values change at runtime. **Ichor-managed Cilium cannot stay in the inline manifest**; this decides option (b) below. (ADR 0024 recorded, from Talos v1.14.3 source, that `manifest_apply.go` server-side-applies on every Kubernetes upgrade. The guide's wording is "creates only". The two readings agree that nothing is pruned; the difference only affects whether a stale inline entry can roll Cilium back. The hand-over below removes the entry either way.)
- v1.14 and later: use a `KubeFlannelCNIConfig` `$patch: delete` document to drop flannel (Ichor does: `pkg/render/networkinstall.go`), `KubeProxyConfig` `enabled: false` instead of `cluster.proxy.disabled`, and the inline-manifest document instead of the deprecated `cluster.inlineManifests`. `Preset.TalosPatches` still emits the legacy `cluster.proxy.disabled` and render strips it; this ADR cleans that up. The exact kind name of the v1.14 inline-manifest document (`KubeInlineManifestConfig`) is taken from the audit of the same guide; work unit 2 verifies it against the pinned machinery before use.
- The guide recommends inline manifests for production, only on control planes, identical on all of them, and warns that without a CNI the node waits "about 10 minutes" before retrying. Known issue: `bpf.masquerade` with `forwardKubeDNSToHost` can break CoreDNS; `forwardKubeDNSToHost=false` fixes it. Gateway API needs its CRDs installed first, then `gatewayAPI.enabled`, `enableAlpn`, `enableAppProtocol`.
- Differences from today's approach: (1) we use the legacy `cluster.inlineManifests` and `cluster.proxy.disabled` on a v1.14 contract (fixed here); (2) we remove the inline entry after hand-over, which the guide does not discuss and which ADR 0024 verified (kept, retargeted); (3) the guide's hosted/CLI/manifest-apply Helm variants are not used.
- The embedded Cilium 1.18.0 chart ships `values.schema.json` (146,865 bytes). It has **no `additionalProperties: false`**, so a misspelled key passes the schema. Helm already validates against the schema on render, so the existing render path gives type checking for free; an unknown-key check against the chart's own `values.yaml` is added (decision 4).

### Footprint (estimates; nothing was measured on a cluster)

Requests/limits come from the manifests above. Idle working-set figures for Argo CD are typical numbers for a small install and are the main uncertainty; unit 14 measures them.

| Component | Kokumi manifest says | Estimated working set |
|---|---|---|
| Kokumi controller + server | limit 128Mi each | 60-120Mi each; the 128Mi limit is tight while rendering/pulling, so Ichor raises it in the published bundle (unit 4) |
| Kokumi registry | 256Mi | 30-100Mi |
| Argo CD application-controller | none | 200-350Mi |
| Argo CD repo-server | none | 100-250Mi, spikes when it fetches an artifact |
| Argo CD server | none | 80-150Mi |
| Argo CD redis | none | 20-50Mi |
| Total | | about 0.7-1.2 GiB working set, about 1.3-1.7 GiB with sensible limits |

On the 1 control plane (3Gi) + 1 worker (2.5Gi) homelab, Talos and the control-plane components use about 1.2-1.5Gi of the control plane, and Cilium and Ichor add roughly 0.4-0.6Gi across the nodes. With a tainted control plane the stack lands on the 2.5Gi worker: it **fits, with little slack**. A **minimal Argo CD** (Helm chart with `dex.enabled=false`, `notifications.enabled=false`, `applicationSet.enabled=false`, `redis-ha.enabled=false`, no `controller` replicas beyond one) removes dex, notifications and the ApplicationSet controller, about 150-250Mi and 3 pods. It is the default (decision 5). The raw `install.yaml` cannot do that; that is why the chart is embedded instead.

## Decisions

### 1. Always installed, no opt-out

`ichor init` installs Cilium, Kokumi and Argo CD every time. Flags `--cni`, `--argocd`, `--argocd-namespace`, `--kokumi`, `--repo`, `--path`, `--repo-username`, `--repo-password` and `--gitops-dir` go away (`--cni` stays only as a hidden flag that accepts `cilium` for one release so scripts get an explicit message; `--cni-tunnel` stays). The `ichor-system` namespace and Ichor itself are applied as today (ADR 0019). Argo CD, Kokumi and Cilium are Ichor's three bootstrap components.

### 2. Cilium: option (b), a Helm release owned by Ichor

Option (a), keep Cilium in `cluster.inlineManifests` and change values by editing it, is rejected on the Talos guide's own text: Talos creates inline manifests and never updates or deletes them, so a values change would apply only through `talosctl upgrade-k8s`, with no prune. Option (b) is chosen.

- **Bootstrap stays the inline manifest**, rendered from the embedded chart plus the effective values (ADR 0098), delivered as the v1.14 inline-manifest document on a v1.14 contract (gated by `MultidocKubernetesConfigSupported()`, as `networkinstall.go` already gates its flannel choice) and as `cluster.inlineManifests` below it. Only control planes carry it, identical on all, as the guide requires.
- **Adoption.** After the control plane is Ready and Cilium's DaemonSet is Ready, a Helm release `cilium` in `kube-system` is created over the bootstrapped objects with the Helm SDK: `action.Install` with `TakeOwnership: true` (field present in `helm.sh/helm/v3` v3.21.4, `pkg/action/install.go:114`, the version in `go.mod`) and the same chart, release name, namespace and values as the bootstrap render. The result has no object changes; it only records release revision 1 as Secret `sh.helm.release.v1.cilium.v1`. Rendering the bootstrap objects with Helm ownership labels and annotations is not needed and is not done.
- **Operation.** Later changes are `action.Upgrade` with the new effective values: Helm's three-way merge updates changed objects and deletes objects the chart no longer renders (prune), with `Atomic`/rollback on failure so a bad value set does not leave a half-upgraded CNI. `lookup` works (Helm talks to the cluster), so the Hubble certificate reasoning in ADR 0055 (cronJob instead of `helm`) no longer forces a choice; the existing `cronJob` value is kept to avoid changing running clusters, and users may override it.
- **Who runs Helm.** The Helm SDK needs the permissions of everything the chart creates (ClusterRoles, CRDs, DaemonSet, ...), which is cluster-admin in practice (ADR 0059 reached the same conclusion for the installer: `escalate`/`bind` cannot be narrowed). So the controller does not run Helm. It launches a one-shot Job, exactly as the Ichor installer does: image `ghcr.io/maeshinshin/ichor-installer:<version>` (reused; same image, new hidden subcommand `ichor cni helm apply`), its own ServiceAccount `ichor-cni`, created per run, deleted after, with `ttlSecondsAfterFinished`. The Job uses `hostNetwork: true` and `KUBERNETES_SERVICE_HOST=localhost`, `KUBERNETES_SERVICE_PORT=7445` (KubePrism) so it reaches the API server while Cilium restarts; the replaced-mode requirement for KubePrism already makes that port a precondition.
- **Hand-over retargeted.** `CNIHandoverReconciler` (ADR 0024) keeps its shape, condition `CNIHandover` and render-time removal of the inline entry. Its signal changes from "Argo CD reports an Application Synced and Healthy" to "Helm release `cilium` is `deployed` at the effective-values hash and the DaemonSet is ready", read from the release Secret and the DaemonSet. `spec.cni.handover` (`automatic|manual|disabled`) is kept as a deprecated field; `manual` and `disabled` are read as `automatic` (decision 8). The upgrade guard `cniHandoverPending` keeps blocking a Kubernetes upgrade until the entry is gone.
- **Why not a controller-embedded Helm run or a CRD of Argo CD.** Embedding means giving the long-lived controller cluster-admin. An Argo CD Application would hand Cilium to Git or Kokumi, which the user decided against.

### 3. Cilium values: stored on the TalosCluster, merged by Ichor

New field `spec.cni.cilium.values` on `TalosClusterCNI` (an object with `x-kubernetes-preserve-unknown-fields`, append-only, immutable only in its owned subset, below). The effective values are, in order: Ichor defaults (`pkg/cni/cilium/values.go`), then the user's `spec.cni.cilium.values` deep-merged on top, then the **Ichor-owned keys** applied last. The UI shows the effective values read-only and the user values as the editable layer.

Ichor-owned keys (cannot be set by users; a value for one is a validation error naming the key and the reason, not silently ignored): `ipam.mode`, `securityContext.capabilities.ciliumAgent`, `securityContext.capabilities.cleanCiliumState`, `cgroup.autoMount.enabled`, `cgroup.hostRoot` (the Talos guide's requirements), `k8sServiceHost` and `k8sServicePort` (KubePrism), `kubeProxyReplacement` (owned by the kube-proxy switch, `spec.cni.kubeProxy`) and `tunnelProtocol` (owned by `spec.cni.tunnel`, immutable).

### 4. Validation

One function, `cilium.ValidateValues(values, context)`, used by the admission webhook (`internal/webhook/v1alpha1/taloscluster_webhook.go`), the Connect service behind the UI and CLI, and the controller before it launches the Job:

1. Render the embedded chart with the merged values (no cluster, no network). Helm's schema validation against the chart's `values.schema.json` yields type errors with paths. A failed render is an error with Helm's message.
2. Reject any Ichor-owned key.
3. Reject or warn on keys absent from the chart's `values.yaml` tree (the schema does not catch typos). Free-form maps in the chart (`extraArgs`, `annotations`, ...) are exempt.
4. Three semantic guards from the Talos guide: `bpf.masquerade=true` is refused unless the cluster's effective machine config has `machine.features.hostDNS.forwardKubeDNSToHost: false` (CoreDNS breaks otherwise; Ichor does not change the Talos side on its own in this ADR); `gatewayAPI.enabled=true` is refused unless the Gateway API CRDs exist (checked live by the controller, which sets a condition; the webhook cannot check and passes); and in replaced mode a patch that disables KubePrism or moves it off 7445 is refused. The context argument carries what each caller can know; the controller re-checks live before every Job.
5. Size: the merged values must stay under 64 KiB (they are stored on the CR and rendered into the Job).

### 5. Kokumi and Argo CD: applied once, then Argo CD owns them

Bootstrap order, from a fresh `ichor init`:

1. Talos config with the Cilium inline manifest is applied; bootstrap etcd; kubeconfig; Secrets bundle stored (unchanged, ADR 0019 steps 1-4).
2. Cilium becomes Ready (the existing wait). Ichor is applied through the API server (unchanged).
3. `init` creates `IchorInstallation` with a new `spec.stack` (below) and waits. The controller (not the CLI) does 4-8 so there is one implementation for init and for later upgrades.
4. The controller applies the embedded Kokumi manifest (server-side apply, field manager `ichor-bootstrap`) with the transformations in decision 6, then waits for the registry Deployment to be Ready.
5. It renders the Argo CD chart with Helm (release `argocd`, namespace `argocd`, values below), applies it the same way, and waits for the server and repo-server.
6. It creates the Argo CD repo-creds Secret (`type: oci`, the registry URL, `insecureOCIForceHttp: "true"`; no credential in it) and the `default` AppProject if absent. These also ship in the Argo CD bundle so Argo CD keeps them.
7. For each of `kokumi` and `argocd` it publishes a **pre-rendered manifest bundle** to the registry (decision 7) and creates one Order (`delivery.kokumi.dev/v1alpha1`, `promotion.mode: Automatic`, no `spec.render`). Kokumi renders a Preparation from it, its Serving creates Application `kokumi` / `argocd`, and Argo CD adopts the live objects. Because the bundle contains exactly the bytes Ichor applied in 4-5, the first sync shows no diff (the same "adoption produces no diff" rule as ADR 0024). Pre-rendered bundles, not Helm Orders, are used deliberately: the bytes come from Ichor's own Helm SDK and embedded chart, not from the `helm template` inside Kokumi, whose version and default `KubeVersion` Ichor does not control.
8. The policies of decision 10 are applied last, once both namespaces and workloads exist.
9. `IchorInstallation.status.stack` reports per component: published digest, Order ready, Application Synced/Healthy. When both are Healthy, init finishes. This is the stack's hand-over; there is nothing to remove afterwards because nothing was inlined.

Argo CD configuration shipped by Ichor (chart values; user-overridable except where noted): `dex.enabled=false`, `notifications.enabled=false`, `applicationSet.enabled=false`, `redis-ha.enabled=false`, `configs.params."server.insecure"` unset, `configs.cm."application.resourceTrackingMethod"` left at Argo CD's default (Ichor reads it, ADR 0044), `crds.keep=true`. Ichor-owned keys, as for Cilium: the repo-creds Secret, the `default` AppProject, and the `argocd-cm` resource customizations for Ichor's kinds. The Lua health checks of ADR 0046 move into the Argo CD bundle's `argocd-cm`, which resolves that ADR's limitation (Ichor did not own `argocd-cm`; it now does). Argo CD has no external Git repository, so `repositories` is empty.

### 6. Where the manifests come from and their size

Embedded in the `ichor` binary and in the installer image, like Cilium (ADR 0098), each with a `make` fetch target that refuses a file whose SHA-256 differs from the recorded digest:

| Artifact | Form | Size in the binary |
|---|---|---|
| Cilium 1.18.0 | chart `.tgz` (already embedded) | 229 KB |
| Argo CD chart 10.11.6 (app v3.5.6) | chart `.tgz` with `redis-ha` subchart | 238 KB |
| Kokumi 0.17.0 | `install.yaml` | 134 KB raw (store gzipped, 15 KB) |
| Helm's own dependencies | already linked for Cilium | 0 extra |

About 0.4 MB added. The rendered Argo CD bundle is about 1.9 MB raw (the three CRDs dominate), about 0.1 MB as a gzipped tar. That is above what fits comfortably in a machine config, which is why Argo CD and Kokumi are applied through the API server and not through Talos inline manifests. Images are pulled from `ghcr.io`, `quay.io`, `public.ecr.aws` and `docker.io`; air-gapped installs are out of scope (ADR 0090), so nothing is mirrored here.

Transformations applied to the Kokumi manifest before it is applied and published, each a small, tested, deterministic step in `pkg/stack/kokumi`: the registry Service becomes `ClusterIP` (the shipped `NodePort 30050` would expose an anonymous, writable registry on every node); the registry volume becomes an `emptyDir` with a size limit unless the cluster has a default StorageClass (see Risks: artifacts are rebuilt from the Ichor controller on registry loss); the controller and server memory limits are raised to 256Mi; the `kokumi-server-auth` Secret's `admin`/`admin` is replaced by a generated password kept in a Secret `ichor-kokumi-admin` in `ichor-system` and shown once by `ichor init` (a login through Ichor's OIDC issuer, ADR 0094, is future work: Kokumi supports OIDC through its Kitchen resource).

### 7. Publishing into Kokumi, and the data flow for the future "addons" UI

New package `pkg/stack` and a controller section in the `IchorInstallation` reconciler.

- **State.** `IchorInstallation.spec.stack` (new, append-only): `argocd.values` and `kokumi.values` (free-form objects, user layer only), `promotion` (`Automatic` default, or `Manual` so a human promotes in Kokumi's UI). `status.stack.<component>` holds `publishedTag`, `digest`, `orderReady`, `applicationHealth`. This singleton already exists, is cluster-scoped and is the object Ichor upgrades; the addons UI later writes `spec.stack.<component>.values` through a service the same way it writes Cilium's values, and a future third component adds a key, not a new mechanism. Kokumi has no chart, so its `values` initially accept only the keys Ichor maps to patches it controls (memory limits, replica counts); this is stated in the field docs.
- **Render.** Effective values = Ichor defaults, user values, owned keys (decision 3 rules, same validation function with a different chart and owned set). Rendered with the Helm SDK to a single `manifest.yaml`.
- **Publish.** A deterministic tar (sorted entries, zero mtimes, fixed owner) gzipped, pushed as one layer of media type `application/vnd.cncf.flux.content.v1.tar+gzip` to `kokumi-registry.kokumi.svc.cluster.local:5000/ichor/<component>:<chartVersion>-<hash12>`, where the hash covers the rendered bytes, with `org.opencontainers.image.source` and `.version` annotations. The same input always gives the same digest, so a republish after a registry restart restores the same references.
- **Order.** `delivery.kokumi.dev/v1alpha1 Order` `argocd` (namespace `argocd`) and `kokumi` (namespace `kokumi`), `spec.source.oci` = the repository, `version` = the tag, `promotion.mode` from `spec.stack.promotion`, default destination. Changing a value moves `version`, Kokumi renders a Preparation, Automatic promotion moves the Serving, Argo CD syncs. History, approvals and rollback are Kokumi's.
- **Reach.** The controller pushes from inside the cluster; the registry has no authentication, so the policy in decision 10 is what restricts writers. The controller's RBAC gains `orders` in the two namespaces and `get/list` on `servings`/`preparations`; it never writes Argo CD objects.
- **Idempotence and loss.** The reconcile compares the Order's `version` and the registry's manifest digest each time; if the tag is gone (registry rebuilt) it republishes and, if Kokumi does not re-render by itself, bumps an annotation on the Order (unit 6; whether Kokumi re-renders a Preparation whose artifact vanished is an open question).

### 8. Cilium is the only CNI; flannel and custom are removed

User decision: existing flannel/custom clusters may be deleted, so no code path keeps them running.

- **New clusters:** `preset` defaults to `cilium`; the webhook rejects creation with any other value.
- **Stored objects (append-only CRD).** The enum `flannel;cilium;custom`, the `custom` field and its types stay in the schema, marked deprecated in the Go doc comments and in the generated CRD description. Removing an enum value does not trip the installer's additive check (`internal/installer/crdcheck.go` compares served versions, property names, property types, `items` and `additionalProperties`; it does not read `enum`), but removing the `custom` property or its sub-properties would fail with `... would be removed` (line 83), and any removal would also make stored `preset: flannel` objects fail validation on their next update. So **nothing is removed from the schema**, and the enum keeps all three values. The transition rule `self.preset == oldSelf.preset` stays.
- **A stored `preset: flannel|custom` is rejected on update, not ignored.** The webhook refuses any change to such a cluster's `spec` (finalizers, labels, annotations and deletion are allowed) with a message saying the preset was removed in this release and the cluster should be deleted and recreated. Ignoring it would be worse: render would now always strip Talos's flannel document, so the next config apply to a flannel cluster would take its network down. The controllers act the same way: `TalosMachineReconciler` and the renderer refuse to render a cluster whose preset is not `cilium`, set a `CNIUnsupported` condition on the `TalosCluster` and make no change to nodes. Nothing else in the code handles flannel or custom.
- `spec.cni.custom`, `spec.cni.handover=manual|disabled`: stored values are read but have no effect; the webhook rejects setting them to anything but absent / `automatic` on create. `spec.cni.kubeProxy` and `spec.cni.tunnel` stay and work as before.
- Code removed: see "What goes away".

### 9. Kube-proxy replacement now changes the Cilium release itself

`spec.cni.kubeProxy` is already an Ichor-owned input (`kubeProxyReplacement`, `k8sServiceHost`, `k8sServicePort` derive from it). The switch executor (`internal/controller/cni_kubeproxy_switch.go`) currently prints the values the operator must set in Cilium's config and waits for the operator to do it (`ciliumChangeInstructions`). With Ichor owning the release, the CNI stage launches the Helm Job with the new mode, then runs the existing verification (`cilium-config` key and DaemonSet rollout), and only then touches the Talos side (`KubeProxyConfig`, v1.14). The order, the confirmation annotation and the "turn the old side off only after the new side is verified" rule are unchanged. The `ichor-cni` Job is the same one used for a values edit, so a kube-proxy switch and a user edit cannot overlap: the Job takes the existing cluster operation lock.

### 10. NetworkPolicies for the Kokumi and Argo CD namespaces (a separate work unit)

Cilium is up before either namespace exists, so the policies are CiliumNetworkPolicies shipped inside each component's bundle, applied by the controller as the last step of the bootstrap (step 8) and re-applied by Argo CD on every upgrade. The `ichor-system` policies are another agent's work and are not designed here. Each of the `kokumi` and `argocd` namespaces gets a default-deny (ingress and egress) plus explicit allows:

- DNS: egress to `kube-system` `k8s-app=kube-dns` on 53/UDP and 53/TCP, from every pod, with a `dns` rule so the allowed names are visible.
- Argo CD to kube-apiserver: egress `toEntities: [kube-apiserver]` (Cilium's entity, so it works with KubePrism and with the node-local endpoint) from the application-controller, server, repo-server (for the one cluster call it makes).
- Argo CD to the Kokumi registry: egress from the repo-server (and the application-controller, which resolves revisions) to pods `app=registry` in `kokumi` on 5000/TCP; the matching ingress on the registry side.
- Ichor publishing into Kokumi: ingress on the registry (5000) and on `kokumi` to the Ichor controller pod in `ichor-system` (namespace and pod labels), egress from `ichor-system` is the other agent's unit; this unit only provides the receiving rules and names the dependency.
- Kokumi's own needs: controller-manager and server to kube-apiserver; controller-manager to the registry (render and push) and to nothing external, since sources are in-cluster; the server's own port 8080/80 ingress from `host` entity only (for `ichor ui`-style port-forward, which arrives from the node); Argo CD ingress for its server from `host` and, for Ichor's UI reading status, none (it reads CRs through the API server).
- Redis inside `argocd`: ingress only from the application-controller, server and repo-server.
- Intra-namespace health probes: kubelet probes come from the `host` entity and are allowed explicitly.
- Applied "once their namespaces exist": the controller applies them after the workloads' namespaces and Deployments exist, never before, so a default-deny cannot cut off the bootstrap sync.

### 11. Upgrades after hand-over

- **Cilium:** the pinned chart changes with the Ichor release. `ichor upgrade ichor` does not upgrade Cilium on its own; it makes the new chart available (`status.cni.availableChartVersion`, condition `CiliumUpgradeAvailable`). The user starts it (`ichor cni upgrade`, or the UI button) as an operation of the existing kind `cni`, which validates the user's values against the new chart's schema first and fails early if they no longer validate, then runs the Helm Job. Cilium supports upgrading one minor version at a time, so each Ichor release changes the pinned Cilium by at most one minor. Rollback is Helm's.
- **Argo CD and Kokumi:** the new controller (after `ichor upgrade ichor`, ADR 0059) recomputes the bundles from its embedded charts and the stored `spec.stack` values; if the tag differs from the Order's `version`, it publishes and updates the Order. With `promotion: Automatic` Argo CD applies the new Preparation; with `Manual` the new Preparation waits in Kokumi's UI. Argo CD upgrades itself this way (the Application managing Argo CD deploys the new manifests; `ServerSideApply=true` handles the CRD growth), and Kokumi upgrades itself the same way. Since both are Argo CD's after this point, Ichor never runs `kubectl apply` on them again; the first-run apply (decision 5, steps 4-5) happens only when the namespace has no Order of that name.
- A user who edits the Orders or the bundle in Kokumi by hand will see Ichor overwrite the Order's `version` on the next reconcile. The Order is Ichor-owned (label `ichor.maesh.dev/managed`); the values are the supported way to change anything.

### 12. Git tracking detection stays

`pkg/gitopstracking`, ADR 0044 and ADR 0051 are unchanged: the UI and CLI still refuse writes to CRs a user's own Argo CD or Flux tracks, the `spec.gitops` repository connection and PR mode (ADR 0056) stay, and Ichor still reads `argocd-cm` rather than guessing the tracking method. That `argocd-cm` is now normally Ichor's own, but a user may run another Argo CD; the code does not care. `TalosCluster.spec.gitops.mode: strict` keeps meaning "the user's Git is the source". `ichor.maesh.dev` CRs are not placed in Ichor's own Argo CD; Ichor's own Applications track only the Kokumi and Argo CD bundles, so Ichor's own Argo CD never tracks an Ichor CR and never triggers the warning.

## What goes away

Removed (the files are named so each can be deleted by a work unit, with tests):

- `internal/cli/init_gitops.go`, `init_gitops_test.go` (`initArgoCD`, `buildRootApplication`, `buildRepoSecret`, `buildSkeleton`).
- `internal/cli/kokumi.go`, `kokumi_test.go` (the repo-creds helper moves to `pkg/stack`).
- `internal/cli/gitops.go`, `gitops_export.go`, `gitops_export_test.go`, `gitops_handoff.go`, `gitops_handoff_test.go` (the `ichor gitops export|handoff` commands), and `exportCNIApplication`.
- `internal/cli/init.go` flags listed in decision 1 and `initOptions` fields `ArgoCD`, `ArgoCDNamespace`, `Kokumi`, `RepoURL`, `RepoPath`, `RepoUsername`, `RepoPassword`, `GitOpsDir`; `init_plan.go` `CNIPreset` validation (line 268), `init_cni.go` (`flannel` branch, `wantArgoCD`, the Application half of `cniBootstrap`), `init_run.go` (the GitOps steps and the Application wiring), and their tests: `init_cni_test.go`, `init_run_test.go`, `init_leak_test.go`, `helpers_test.go`.
- `pkg/cni/flannel/` (whole directory); `pkg/cni/preset.go`: `ArgoCDApplication`, `HandoverWorkloads` and the Argo-specific doc; `pkg/cni/cilium/cilium.go` `ArgoCDApplication` and its ADR 0055 tests; `Preset.TalosPatches`' legacy `cluster.proxy.disabled`.
- `pkg/gitops` builders and the skeleton (`skeleton.go`, `application.go` `New`) after confirming `pkg/prmode/source.go` only needs the Application read types (it does not use the builders).
- `internal/controller/cnihandover_controller.go`: the Argo CD lookup (`argoApplicationGVK`, `argoApplicationStatus`, `WaitingForArgoCD` reasons), `cniPresetFor`'s flannel branch; `internal/controller/kubernetes_upgrade.go` (`cniHandoverPending` comment and flannel case), `cni_kubeproxy_switch.go` (`ciliumChangeInstructions` and its Argo wording), `etcd_restore.go` (`kube-flannel` in `restoreAgentNames`), `ca_rotation_clients.go` and `config/rbac/role.yaml` (`kube-flannel` resourceName), and the `cni` rule markers that name Argo CD.
- `pkg/render/networkinstall.go`: `talosRunsFlannel`, `cniPresetFlannel`, the `preset == flannel` branch; the flannel document is always removed. `internal/cli/adopt_run.go` lines 222-223: `adopt` of a flannel cluster is refused with the deletion message (adopt keeps working for Cilium clusters).
- Docs: architecture.md ("Bootstrap" step 3, the whole "GitOps and Argo CD" hand-over text, "Kokumi as the delivery layer in front of Argo CD", "CNI" presets text, the Open questions entry on Kokumi) and the repository skeleton block; ADRs 0013, 0019, 0024, 0028, 0048, 0055 gain a "superseded in part by 0099" line.
- Kept: `pkg/gitopstracking`, `pkg/prmode`, `spec.gitops.*`, `internal/uiapi/gitops_*`, the UI GitOps page, `dist/chart` `argocdHealth` (its Lua files now also feed the bundle's `argocd-cm`).
- Kept as deprecated, unused CRD fields (append-only): `spec.cni.preset` values `flannel`/`custom`, `spec.cni.custom` and `TalosClusterCNICustom*` types, `spec.cni.handover` values `manual`/`disabled`, `TalosOperation` fields that mention handoff.

## Work units

Units are small and mostly independent; each names its files. Dependencies are in brackets. Commit incrementally, one Conventional Commit per unit.

1. **CRD fields and deprecations.** `api/v1alpha1/taloscluster_types.go` (add `spec.cni.cilium.values`, change the `preset` default to `cilium`, deprecate `custom`/`flannel`/`handover` values in comments), `api/v1alpha1/ichorinstallation_types.go` (`spec.stack`, `status.stack`), `make manifests generate`, `config/crd/bases/*`, `dist/chart`. Check `internal/installer` additive test still passes against the previous CRDs. [none]
2. **Talos v1.14 render cleanup.** `pkg/cni/cilium/cilium.go` (`TalosPatches` emits `KubeProxyConfig enabled:false`), `pkg/render/networkinstall.go`, `pkg/render/kubeproxy.go`, the inline-manifest document on a v1.14 contract (verify the kind in the pinned machinery), `pkg/fieldmeta/fieldmeta.go`, tests. [none]

   Unit 2 as built: the kind is `KubeInlineManifestConfig` (apiVersion `v1alpha1`, Go type `k8s.KubeInlineManifestConfigV1Alpha1` in `talos/pkg/machinery/config/types/k8s/inline_manifest.go`, v1.14.3; fields `name`, `manifest`). Patches keep carrying the legacy `cluster.inlineManifests` entries (the typed CRD has no document kind for it); `applyPatches` converts them to that document on a `MultidocKubernetesConfigSupported()` contract, and the hand-over removes the entry before conversion, so it works for both forms. `Preset.TalosPatches` no longer emits `cluster.proxy.disabled`; `injectKubeProxy` is the only owner. Hash effect: a v1.14-contract render with a patch inline manifest (every cluster with the CNI entry) changes from `cluster.inlineManifests` to a `KubeInlineManifestConfig` document, so its hash moves once (a rolling config apply, safe class via a new `fieldmeta` rule). Contracts below v1.14 render byte-identically; v1.14 renders without inline manifests are unchanged.
3. **Cilium-only code removal.** Delete `pkg/cni/flannel/`, flannel/custom branches in `internal/cli/init_cni.go`, `init_plan.go`, `internal/controller/*` listed above, `config/rbac/role.yaml`. Webhook: create requires `cilium`, update of a stored flannel/custom `spec` refused, `CNIUnsupported` condition and render refusal. `internal/webhook/v1alpha1/taloscluster_webhook.go`, `pkg/render`, `internal/controller/talosmachine_controller.go`. [1]
4. **`pkg/stack` (render and publish).** New package: embedded Argo CD chart and Kokumi manifest with fetch targets and digests (`Makefile`, `pkg/stack/argocd/chart/`, `pkg/stack/kokumi/install.yaml.gz`), Kokumi transformations, Argo CD default values, deterministic bundle tar, OCI push (oras-go or go-containerregistry; pick by Context7 docs), an in-memory registry fake for tests. [none]
5. **Values engine.** `pkg/values` shared by Cilium, Argo CD and Kokumi: merge order, owned-key rejection, unknown-key check, schema validation, size cap; `pkg/cni/cilium/validate.go` with the three semantic guards. [none]
6. **Stack controller.** `internal/controller/stack.go` inside the `IchorInstallation` reconciler: steps 4-9, Orders (unstructured, no Kokumi Go dependency, like the Argo CD lookups today), status, republish on registry loss, RBAC markers, `config/rbac`, `dist/chart`. [1, 4, 5]
7. **Cilium Helm Job.** Hidden `ichor cni helm apply` in `internal/cli`, Job builder and watcher in `internal/controller/cni_helm.go`, SA `ichor-cni` RBAC in `config/rbac/ichor-installer` style with the chart-comparison test, adoption with `TakeOwnership`, upgrade and rollback, lock acquisition. [1, 5]
8. **Hand-over retarget.** `internal/controller/cnihandover_controller.go`, `render` (unchanged removal), `desiredInputsHash` unchanged, `kubernetes_upgrade.go` guard, tests; `spec.cni.handover` read as automatic. [3, 7]
9. **Kube-proxy switch drives the release.** `internal/controller/cni_kubeproxy_switch.go`, `cni_kubeproxy_admin.go`, tests. [7]
10. **`ichor init` rewrite.** `internal/cli/init.go`, `init_run.go`, `init_plan.go`, `init_cni.go`; create `IchorInstallation.spec.stack`; wait on `status.stack`; print the Kokumi admin password once; delete the flags and files above. [3, 6]
11. **`ichor cni values|upgrade` commands and service.** `internal/cli/cni.go` (`values get|set|edit|diff`, `upgrade`), `proto/ichor/v1alpha1` (CniService), `internal/uiapi/cni_service.go` (effective values, validate, diff preview, apply: writes `spec.cni.cilium.values`, honours tracking refusal ADR 0051). [1, 5]
12. **UI Cilium settings.** `ui/src/pages/Cilium.tsx` (effective values read-only view, YAML editor for the user layer, validate, diff preview, apply), route in `ui/src/router.tsx` and `app.tsx`, nav entry; reuse the config-editing patterns of ADR 0039; preactcn only. A later `Addons` page uses the same editor against `spec.stack`. [11]
13. **NetworkPolicies for `kokumi` and `argocd`.** `pkg/stack/policies/*.yaml` (CiliumNetworkPolicies per decision 10) included in each bundle, applied last by unit 6, unit tests that parse them and a render test checking default-deny plus each named allow; e2e connectivity check with Cilium. Does not touch `ichor-system` policies. [4, 6]
14. **e2e on the e2e harness.** `test/e2e`: init to Healthy stack, a Cilium values edit applied live, a kube-proxy switch, an Argo CD values edit through Kokumi, a registry restart, measured memory of the stack on the 3Gi + 2.5Gi layout, plus the three unanswered Argo CD points. Hardware batch per the usual rule. [10, 7, 6]
15. **Docs.** `docs/design/architecture.md` rewrite of the sections above, supersession lines in ADRs 0013, 0019, 0024, 0028, 0048, 0055, `docs/adr/README.md` via `make adr-index`. [alongside each unit]
16. **Removal of GitOps export.** Delete `internal/cli/gitops*.go`, `pkg/gitops` builders and the skeleton, UI strings for export, once unit 10 no longer calls them. [10]

Units 1, 2, 4 and 5 can start at once on stub commits; 3 follows 1; 6 and 7 follow 1, 4, 5; 8-13 follow their brackets.

## Consequences

- One path for every cluster: Cilium plus Kokumi plus Argo CD. Nothing to choose, nothing to explain, and the e2e covers one shape.
- Ichor's controller now runs two kinds of Job (installer and Cilium Helm) and writes Kokumi Orders. The controller still never writes Talos nodes outside the machine controller and still never mutates `spec`.
- Cilium is editable from the UI because Ichor, not Git, holds its values. Argo CD never manages Cilium, so a Cilium change cannot be reverted by Argo CD. The cost is a cluster-admin-equivalent Job identity for a few minutes per change (same exposure as the installer).
- The user's own apps get Argo CD with no repository set up; they can add repositories to it. They still must not put Ichor CRs under it (the warning stays).
- Ichor now ships three third-party releases and bumps them: an Ichor release carries Cilium, Argo CD and Kokumi pins, each with a digest in the Makefile.
- Memory on the small homelab is tight (estimated 1.3-1.7 GiB for the stack, a 2.5Gi worker), and the default Kokumi limits of 128Mi are probably too low.
- ADR 0048's "Unknowns" are answered: the Application accepts only what Kokumi generates, so sync waves and `ignoreDifferences` cannot be carried (not needed now that Cilium is not delivered through Kokumi); the Product/Order question is answered by bundle-per-component Orders; air gap is out of scope (ADR 0090); the Argo CD range is >= 3.3 and 3.5.6 is pinned; Kokumi remains experimental `v1alpha1`.

## Risks

- **Kokumi is experimental** (`v1alpha1`, breaking changes in every minor release: AutoDeploy removed in 0.17, Menu made namespaced in 0.16, status fields removed in 0.15 and 0.13). Ichor pins 0.17.0 and must re-read the source for each bump. A broken Kokumi stops value changes for Argo CD and Kokumi but not the running workloads, and does not affect Cilium or the nodes.
- **Registry durability.** The default emptyDir loses the registry on a pod restart or node reboot. Source bundles and the pinned digest are reproducible, but Kokumi's Preparations (rendered artifacts) are not; if Kokumi does not re-render, Argo CD fails to fetch the revision until the Order is bumped. Using a PVC needs a StorageClass Ichor does not provide.
- **Self-management.** Argo CD prunes and self-heals its own manifests: a wrong bundle can remove Argo CD or the Kokumi registry. Mitigations: pre-rendered bundles are identical to the applied bytes, `Automatic` promotion can be set to `Manual`, and Kokumi Applications carry no resources-finalizer.
- **Bootstrap-time diffs.** First sync of an adopted component could show a diff if the bundle and the applied objects differ in defaulted fields; server-side apply makes that benign but the first Healthy takes longer.
- **Anonymous writable registry.** Any pod could push a source bundle that Argo CD later applies cluster-wide; the CiliumNetworkPolicies (decision 10) are the only barrier until Kokumi's `requireSourceVerification` is used with a key held by Ichor (future).
- **The Cilium Job path.** A Cilium upgrade replaces the CNI on live nodes; a Job that fails mid-way relies on Helm's atomic rollback. The Job uses KubePrism; if a cluster has KubePrism disabled the Job uses the in-cluster service address and can lose its API access during the agent restart.
- **Resource pressure** on small clusters (above), including Kokumi's 128Mi limits and Argo CD repo-server spikes.
- **Values footguns.** The schema does not reject unknown keys; the unknown-key check is best effort and some Cilium values only work with others.

## Open questions

- Does Kokumi 0.17.0 re-render a Preparation whose artifact has vanished from the registry, or must the Order be changed? (Unit 6 and the e2e.)
- Does Argo CD accept the Preparation artifact's layer media type (Kokumi pushes `application/vnd.kokumi.preparation.v1`) with its default `ARGOCD_REPO_SERVER_OCI_LAYER_MEDIA_TYPES`? Kokumi's own Getting Started works with Argo CD 3.5.1; our version is not tested.
- How large a bundle can Argo CD take from an OCI source (the docs name no limit; the Argo CD bundle is about 1.9 MB raw)?
- Is `hostNetwork` plus `localhost:7445` the right way for the Cilium Job to keep its API access, or should the Job tolerate a short API outage and retry?
- Should the registry use a PVC when a default StorageClass exists, and which size? 1Gi is Kokumi's default; Preparations are never garbage collected.
- Should Ichor render the Talos side of `bpf.masquerade` (`forwardKubeDNSToHost: false`) itself instead of refusing the value?
- Whether to wire Kokumi's login to Ichor's OIDC issuer (ADR 0094) in this change or later; until then the generated admin password is the credential.
- Does `ichor adopt` of an existing Cilium cluster need an adoption path for an Argo CD-managed Cilium Application (a cluster that went through ADR 0024)? It is out of scope here; those clusters are re-created like flannel ones unless the user asks for it.
- The exact Talos v1.14 inline-manifest document kind and its fields must be confirmed against the pinned machinery (unit 2).
