# 0081. The UI demo cluster is built by `ichor init`, pulling Ichor's images from a throwaway in-namespace registry

Status: Accepted · Date: 2026-10-26 · Design section: Bootstrap

Decided: The demo cluster's control plane is created by `ichor init` and its worker approved through `ichor machine approve --install-disk`, so every node's configuration is rendered by Ichor; images are pushed with `crane` to a `registry:2` Deployment in `ichor-e2e` and reached through an anonymous `spec.registries` mirror of `ghcr.io` that `ichor init --registry-mirror` writes; `init` also gains `--pod-subnets` / `--service-subnets`, waits for the node, the API server and the webhooks, and applies the install manifest before the bundle Secret; the controller's shipped RBAC and cache are made consistent.

## Context

The previous demo (`up-demo`) installed Talos with a config built by the test harness, so Ichor's render differed from the node's
config and its control plane could never converge (ADR 0077's drift). The user wanted the demo built with the product's own
bootstrap. Doing that on a workstation with no container runtime and no registry exposed product gaps.

## Decision

1. **Harness.** `up-demo` only creates the two nodes (name prefix from `ICHOR_UI_DEMO_PREFIX`, default `ichor-ui-demo`)
   and leaves both in maintenance mode. `ichor init` runs from a busybox pod in `ichor-e2e` because the workstation cannot reach pod IPs.
2. **Images.** `crane` (added to `flake.nix`) appends the statically built `manager` / `ichor-ui` binaries as a layer on
   `gcr.io/distroless/static:nonroot` and pushes them through a `kubectl port-forward` to a `registry:2` Deployment+Service
   (`<prefix>-registry`, emptyDir, in `ichor-e2e` only). The install manifest is built with `make build-ichor-embed IMG=ghcr.io/maeshinshin/ichor-controller:<tag>
   IMG_UI=ghcr.io/maeshinshin/ichor-ui:<tag>`, so the image references stay real `ghcr.io` names and the nodes reach the registry
   only through `TalosCluster.spec.registries`: host `ghcr.io`, endpoint `http://<registry ClusterIP>:5000`. containerd tries the mirror
   first and falls back to `ghcr.io` for everything else (Talos's own images). No raw YAML and no patch: the typed field already existed.
3. **`ichor init --registry-mirror HOST=URL`** (repeatable) fills `spec.registries` with anonymous mirrors. `init` used to refuse any
   registry because a new cluster has no Secret to resolve credentials from; it now refuses only an entry carrying `authSecretRef`,
   which the flag cannot produce.
4. **`ichor init --pod-subnets` / `--service-subnets`** set `spec.network` (ADR 0077); each is validated as at most two CIDRs.
   The demo uses 10.248.0.0/16 and 10.249.0.0/16 because the outer cluster owns 10.244.0.0/16.
5. **`init` ordering and waiting**, all found by running it against a real node. Bootstrapping etcd retries while the node answers
   `Unavailable` (still serving its maintenance certificate) or `FailedPrecondition` ("bootstrap is not available yet"), up to 15 minutes.
   `init` waits for the API server before the first write. It applies Ichor's manifest before the bundle Secret, because the Secret lives
   in the namespace the manifest creates (it failed with "namespace not found"). The CRs are created with a retry while the API server
   cannot call Ichor's `failurePolicy: Fail` webhooks, whose controller `init` has only just started.
   The bootstrap `TalosConfigPatch` (it carries `machine.type` and `certSANs`) moves from the `cluster` layer to the `role` layer
   targeting the first node's role: at the cluster layer it forced every later machine, a worker included, to render as a control plane.
   An existing cluster made by an older `init` keeps the cluster-layer patch; recreate it with `layer: role` and `target.role: controlplane`.
6. **Controller RBAC and cache.** The shipped Role grants Secret `list`/`watch` in `ichor-system` only and the webhook configuration
   only by `resourceNames`, but the manager's cache listed Secrets cluster-wide and cert-controller's informer lists every
   `ValidatingWebhookConfiguration`; both were forbidden, the cert rotator's cache never synced and the pod crash-looped (the old demo hid this
   behind `cluster-admin` and `ENABLE_WEBHOOKS=false`). The Secret informer is now confined to `POD_NAMESPACE`, and `list`/`watch` on
   `validatingwebhookconfigurations` are granted without `resourceNames` (an informer cannot name its object); `get`/`update`/`patch` stay
   restricted to Ichor's one configuration. This widens the controller's read access to webhook configurations cluster-wide, read-only.

## Not done

- The registry is plain HTTP and unauthenticated, reachable by anything in the outer cluster; it is a demo fixture.
- `crane` pushes the base image's layers too, so the registry holds a copy of the distroless base.
- The demo uses the `node` endpoint type with the node's pod IP, which changes if the node restarts; a restart needs a re-init.
