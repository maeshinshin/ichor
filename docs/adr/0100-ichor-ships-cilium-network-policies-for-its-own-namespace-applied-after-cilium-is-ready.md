# 0100. Ichor ships CiliumNetworkPolicies for its own namespace, applied after Cilium is Ready

Status: Accepted · Date: 2026-10-29 · Design section: Bootstrap, Upgrades (Ichor) · Extends: 0059, 0094, 0098

Decided: `ichor-system` gets a default deny and one explicit allow per flow the controller, ichor-ui and the installer Job need, written as `CiliumNetworkPolicy` with the entities `kube-apiserver`, `host`, `remote-node`, `world` and `cluster`; the policies are built from `config/cilium-network-policy` into `dist/network-policies.yaml`, embedded next to the install manifest, applied by `ichor init` after Cilium is Ready and by the installer Job on an upgrade when Cilium's CRD exists, and rendered by the chart behind `networkPolicy.cilium.enabled`

## Context

Cilium is the only CNI (ADR 0098), so a policy engine is guaranteed. A plain `NetworkPolicy` cannot say "the API server", "a Talos node" or "the kubelet": those run on the host network, and their pod-network identity depends on the CNI. Cilium names them as entities. The chart's older `networkPolicy.enabled` templates (kubebuilder scaffolding) restrict ingress only, by namespace label, and are not changed.

The `CiliumNetworkPolicy` CRD exists only once Cilium runs, so the policies cannot be in `dist/install.yaml`, which `ichor init` applies before the node has a CNI (ADR 0019). Scope is Ichor's namespace only; Kokumi's and Argo CD's are left to their own owners (ADR 0099).

## What the docs confirm about Talos and Cilium

Read for this decision (Talos v1.14, Cilium 1.18):

- Cilium, Policy language, entities (`https://docs.cilium.io/en/v1.18/security/policy/language/`, layer 3 "Entities based"): `host` is the local host and "also includes all containers running in host networking mode on the local host"; `remote-node` is any other node "also includes all containers running in host-networking mode on remote nodes"; `kube-apiserver` "represents both deployments of the kube-apiserver: within the cluster and outside of the cluster"; `world` is everything outside the cluster; `cluster` contains the host, remote-node, kube-apiserver, health, ingress and every endpoint. The only documented caveat is that `kube-apiserver` may not work for *ingress* on AKS and GKE, where control-plane traffic is tunnelled through workers and loses its source IP; egress works everywhere. Talos's apiserver reaches pods directly from a control plane node, which is not that case.
- Cilium, identities (`https://docs.cilium.io/en/v1.18/gettingstarted/terminology/`, "Special Identities"): `reserved:kube-apiserver` is "Remote node(s) which have backend(s) serving the kube-apiserver running", and the metrics page says an identity can carry `kube-apiserver` together with `host`, `remote-node` or `world`. So the apiserver on the local control plane is `host` plus `kube-apiserver`, on another one `remote-node` plus `kube-apiserver`, and an external one `world` plus `kube-apiserver`. A rule on `kube-apiserver` matches all three; a rule on `host` alone would also match the local apiserver.
- Cilium, default deny (`.../security/policy/layer3`): "any rule selecting the endpoint and containing the ingress or egress section" puts that direction in deny mode; `egress: [{}]` and `ingress: [{}]` is the documented empty rule. Kubernetes' local-host-to-pod traffic (probes) stays allowed unless `--allow-localhost=policy`; the policies still name `host` for the probe port so they hold if that is changed.
- Talos, KubePrism (`https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/system-configuration/kubeprism.md`): "a TCP loadbalancer on every machine on the `localhost`", port 7445 by default. The Talos Cilium guide (`https://docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium.md`) sets `k8sServiceHost=localhost`, `k8sServicePort=7445` for the Cilium agent, which runs on the host network, so the agent reaches the API server over KubePrism. **Ichor's pods do not use KubePrism**: `localhost` inside a pod is the pod. They use the in-cluster `kubernetes` Service (`KUBERNETES_SERVICE_HOST`), which Cilium translates to the apiserver backends (the control plane nodes, port 6443) before policy is checked. KubePrism is therefore irrelevant to these policies, and no rule mentions 7445.
- Talos, network connectivity (`https://docs.siderolabs.com/talos/v1.14/learn-more/talos-network-connectivity.md`): apid listens on TCP 50000 on every node, not configurable.

**Not confirmed by those pages**, and listed in the hardware checklist instead: that Talos's kube-apiserver is a host-network static pod (the fetched Talos pages do not say; it is how Talos runs it), and how Cilium learns the apiserver backends (the Cilium pages do not say; the identity's definition says "remote nodes with backends serving the kube-apiserver"). The rules below hold only if the entities match the real traffic, which hardware must show.

## Decision

Four `CiliumNetworkPolicy` objects, names prefixed `ichor-`, in `config/cilium-network-policy/policies.yaml`:

| Policy | Selects | Purpose |
|---|---|---|
| `default-deny` | every pod in the namespace | ingress and egress deny |
| `controller-manager` | `control-plane=controller-manager`, `app.kubernetes.io/name=ichor` | the 2 controller replicas |
| `ui` | `control-plane=ui-backend`, `app.kubernetes.io/name=ichor` | the 2 ichor-ui replicas |
| `installer` | `app.kubernetes.io/name=ichor-installer` | the installer Job (new pod label) |

### Flows

| Workload | Direction | Peer | Port | Why |
|---|---|---|---|---|
| controller | ingress | `kube-apiserver` | 9443/TCP | the validating webhooks (ADR 0006); the apiserver is on a control plane's host network |
| controller | ingress | `host` | 8081/TCP | kubelet startup, liveness and readiness probes |
| controller | ingress | `cluster` | 8443/TCP | `/metrics`, itself behind TokenReview and SubjectAccessReview. In-cluster scrapers only; nothing outside the cluster scrapes |
| controller | egress | `kube-apiserver` | 6443/TCP | Kubernetes API through the `kubernetes` Service, watches, leader election |
| controller | egress | `host`, `remote-node`, `world` | 50000/TCP | Talos apid on every node. `world` because discovery and adoption dial machines that are not Nodes yet (maintenance mode) |
| controller | egress | same | 6443/TCP | `APIServerReady` GETs `https://<node>:6443/readyz` on a node's own address, also one that is not a member yet |
| controller | egress | kube-dns pods (`kube-system`, `k8s-app=kube-dns`) | 53/UDP, 53/TCP | resolving node, S3 and registry host names |
| controller | egress | `world` | 443/TCP | `TalosEtcdBackup`'s S3 endpoint (`internal/etcdbackup`: any `http(s)://host[:port]`) |
| ichor-ui | ingress | `world`, `cluster` | 8080/TCP | users through a LoadBalancer or NodePort, an in-cluster ingress or gateway, Prometheus (`/metrics`), the kubelet's probes and `ichor ui` port-forwards (host) |
| ichor-ui | ingress | `kube-apiserver` | 8444/TCP | OIDC discovery and JWKS of Ichor's own issuer, read by the apiserver over the Service ClusterIP (ADR 0094) |
| ichor-ui | egress | `kube-apiserver` | 6443/TCP | Kubernetes API, with impersonation when OIDC is on |
| ichor-ui | egress | `host`, `remote-node`, `world` | 50000/TCP | node logs, support bundles, etcd snapshots, packet captures, discovery dial apid |
| ichor-ui | egress | kube-dns | 53 | name resolution |
| ichor-ui | egress | `world` | 443/TCP | the external OIDC issuer (`go-oidc` fetches its discovery document and JWKS) and the Git host of a pull request (`pkg/prmode`, GitHub or Enterprise) |
| installer | egress | `kube-apiserver` | 6443/TCP | everything it does is API calls; no DNS (the Service is an IP), no nodes |

Nothing else: no egress to registries (the kubelet pulls images, not pods), no etcd, no Argo CD or Kokumi (ichor-ui reads their objects through the API server), no trustd (the controller's code says apid is the only port it dials, ADR 0011), no ingress to the installer.

Choices worth stating:

- **Ports on entities.** The apiserver rule is limited to 6443 (Talos's fixed port) rather than any port. The apid rule includes `world` because the controller must reach machines before they join; it is bounded by the port.
- **Webhook and issuer ingress allow only `kube-apiserver`**, not all of `host` and `remote-node`: the documented identity carries that label alongside the host one. If hardware shows the label is missing on a distribution, the fallback is to add `host` and `remote-node` to those two rules; the `cluster` entity must not be used there.
- **8080 and 8443 take `cluster`**, which includes the host, other pods and Cilium's ingress proxy, so Prometheus and an ingress controller in any namespace work without Ichor knowing their labels. 8080 also takes `world` because a LoadBalancer with `externalTrafficPolicy: Local` or a native-routing Cilium keeps the client address. The UI authenticates every request when users or OIDC are on; with both off it already trusts whoever reaches it (ADR 0058), and a policy cannot fix that.
- **External ports are 443.** An S3 store, OIDC issuer or Git host on another port, or inside the cluster, needs a policy of the operator's own; policies add up. The chart takes `networkPolicy.cilium.externalPorts` for the same list.
- **No DNS proxy.** The DNS rule is port-level to kube-dns; `toFQDNs` would put every query through Cilium's DNS proxy and cannot name a user-configured S3 endpoint in advance.

### Delivery

- **Sources.** `config/cilium-network-policy` is a kustomization of its own (namespace `ichor-system`, prefix `ichor-`), not part of `config/default`. `make build-installer` also writes `dist/network-policies.yaml`; `build-ichor-embed` and `docker-build-installer` copy it to `internal/installmanifest/network-policies.yaml` (gitignored, like `install.yaml`), embedded as `installmanifest.NetworkPolicies` with the same placeholder in builds without `-tags installembed`.
- **`ichor init`** applies them after step 5 (the CRs exist), before the authentication switch and Argo CD: it waits for the `kube-system/cilium` DaemonSet to have all its scheduled agents ready and for `ciliumnetworkpolicies.cilium.io` to be Established, then creates each policy through `createWithRetry` (`retryInit`). With `--cni flannel` it prints a notice and skips: Flannel does not enforce them. Failing here fails init with a message that says the policies were not applied. Applying them last means the webhook and metrics paths are proven working before it.
- **The installer Job** applies them as its last step, after the UI, in the same snapshot and rollback, and only when the CRD is Established; otherwise it logs and skips. Its Role gains `ciliumnetworkpolicies` in the namespace (`config/rbac/ichor-installer`, the chart copy compared by the existing test). The Job's pod template now carries `app.kubernetes.io/name: ichor-installer`, which the `installer` policy selects; a Job pod without it would be cut off from the API server by the default deny. An old controller launching a new installer (an upgrade from before this ADR) has no policies installed yet, so nothing blocks the first run.
- **The chart** renders the same policies from values behind `networkPolicy.cilium.enabled` (default false, as Cilium's CRD must exist): ports come from `webhook.port`, `metrics.port`, `ichorUI.port` and `ichorUI.users.internalPort`; rules for a disabled webhook, metrics endpoint, users, UI or installer are omitted. Probe port 8081 is fixed in the chart's Deployment. A Helm or Argo CD install that turns this on must have Cilium running first.

### Keeping it correct

`dist/chart/netpol_test.go` renders the chart and parses the kustomize policies: it fails when the chart's and the kustomize specs differ, when a policy's selector is not a subset of its Deployment's pod labels, when an ingress port is not the one the Deployment's flags or ports open (chart: from non-default values; kustomize: from the manifests' own text and `installer.UIInternalPort`). `internal/cli` tests init's ordering and `internal/installer` tests (against envtest) that the policies are applied last and only with the CRD.

## Consequences

- A cluster that runs other workloads in `ichor-system` has them denied too: the namespace is Ichor's.
- Upgrading Cilium or changing the apiserver's address does not need a policy change; moving the Kubernetes API off port 6443 or apid off 50000 does.
- The policies are applied, not reconciled: a manual edit stays until the next upgrade. The controller reconciling them was left out to keep it from writing network policy to the cluster it runs in.
- A pod that cannot start because of a bad policy is repaired with `kubectl delete ciliumnetworkpolicy -n ichor-system ichor-default-deny`; the installer or init applies it again.

## Not verified

Everything about real traffic. To check on a Talos cluster with Cilium (`kubectl -n ichor-system`, `cilium-dbg monitor --type drop` or Hubble `hubble observe --verdict DROPPED -n ichor-system` on the node):

1. `ichor init --cni cilium` finishes, prints "applied Ichor's network policies (4 objects)", and `get ciliumnetworkpolicy` lists four; the CR creation step before it already proved the webhook path works with no policy, so then create and edit a `TalosConfigPatch` after init to prove it with them.
2. No DROPPED verdicts for the controller and ichor-ui pods at steady state; both Deployments stay Ready (probes) across a pod restart.
3. Webhook: a `TalosCluster` update is admitted from a client on a node that is not the apiserver's own (apiserver on another control plane than the controller pod, then on the same one). If dropped, the source identity is shown in the drop line: add `host`/`remote-node` as in the Decision.
4. `cilium-dbg identity list` (or `cilium-dbg ip list`) shows the control plane node IPs with `reserved:kube-apiserver` plus `host`/`remote-node`.
5. Controller: node reads and an operation work (apid 50000), `APIServerReady` during a node upgrade (6443 on a node address, including a node being added), a `TalosEtcdBackup` to an S3 endpoint on 443, DNS resolution of a host name endpoint.
6. ichor-ui: the UI loads through a LoadBalancer and through a NodePort from outside the cluster (note which entity the drops show if not), `ichor ui` port-forward, `/metrics` scraped from another namespace, node logs and a support-bundle download (50000), OIDC login with an external issuer (443), a Git pull request.
7. Users (`ichor init --ui-url`): the apiserver fetches `https://<ClusterIP>:8444/.well-known/openid-configuration` and the JWKS (the 8444 rule); `kubectl` with an `ichor auth token` credential works.
8. `ichor upgrade ichor` from a version with these policies to the next: the installer Job runs (its pod has the label), applies the Deployments and then the policies, and finishes; the same on a cluster without Cilium's CRD skips them with a log line.
9. Negative checks: a pod in another namespace cannot reach 9443 or 8444 on the controller or ichor-ui pods; the controller pod cannot reach an arbitrary external port (for example 8080 on the internet).
10. A rolled-back installer run restores or removes the policies listed in its snapshot.

## Amendment (2026-10-29): webhook and issuer ingress also allow remote-node

Hardware (1 CP + 1 worker, Cilium 1.18.0, geneve) showed the `kube-apiserver` entity does not match the apiserver's calls to a pod on another node: they arrive SNATed to the control plane's `cilium_host` address, which the receiving agent labels `reserved:remote-node` only (on its own node that address is `host` + `kube-apiserver`). Admission on 9443 timed out whenever the controller ran off the apiserver's node. The 9443 (webhook) and 8444 (OIDC discovery/JWKS) rules now allow `kube-apiserver` and `remote-node`; with both, edit and delete through the webhook succeeded across nodes. `remote-node` is node host-network traffic only, never pods in other namespaces, so the negative checks (a pod elsewhere cannot reach 9443/8444) still hold.

## Amendment (2026-10-29): no policy for the Cilium Helm Job, which now runs in kube-system

Hardware (Talos v1.14, fresh cluster): the `ichor-cni` Job never ran, `pods "ichor-cni-..." is forbidden: violates PodSecurity "baseline:latest": host namespaces (hostNetwork=true)`. The Job needs `hostNetwork` (it reaches the API server through KubePrism on `localhost:7445` while Cilium restarts, ADR 0099), and `ichor-system` is enforced at Talos's default `baseline`. Talos's default `KubeAdmissionControlConfig` for `PodSecurity` is `enforce: baseline`, `audit` and `warn: restricted`, with `exemptions.namespaces: [kube-system]` (Talos v1.14, `reference/configuration/kubernetes/kubeadmissioncontrolconfig`, and the `PodSecurity` example under "Patching"). The Job, its ServiceAccount `ichor-cni` and the controller's namespaced Role for it (`jobs`, `pods`, `pods/log`, `events`) therefore live in `kube-system`, the namespace Cilium itself runs in; Helm's release record Secret stays in `ichor-system` and its Role there now binds the `kube-system` ServiceAccount.

The `cni` policy (egress to `kube-apiserver` 6443 and `host` 7445) is removed from `config/cilium-network-policy` and the chart rather than moved. Cilium's documentation (troubleshooting, "Ensure pod is managed by Cilium", and Host Policies) says a pod in host networking is not managed by Cilium, "ingress and egress policy rules selecting the respective pods will not be applied", and only a Host Policy (a `CiliumClusterwideNetworkPolicy` with `nodeSelector`) governs it. A `CiliumNetworkPolicy` in `kube-system` selecting the Job's pod would select nothing in effect, so it would only suggest a restriction that does not exist. A host policy would apply to the whole node and is out of scope (this ADR covers Ichor's namespace). The remaining policies are `default-deny`, `controller-manager`, `ui`, `installer` and `stack`. The installer and ichor-stack Jobs do not use `hostNetwork`; their pods (non-root, `RuntimeDefault` seccomp, no privilege escalation, all capabilities dropped, read-only root filesystem) already satisfy the `restricted` profile that `ichor-system` would be audited against.

## Amendment (2026-10-29): policies are applied only where Cilium is detected

ADR 0103 gates the policies on `status.cni.detected`: `ichor init`, the adopt install step and the installer Job apply the `CiliumNetworkPolicy` objects only when the detected CNI is `cilium` and the CRD exists. On an adopted cluster with another CNI, `ichor-system` is not isolated by Ichor and the condition `NetworkPoliciesApplied` says so. The chart default stays `networkPolicy.cilium.enabled: false` (the installer, not the chart, applies them); the installer does not read it, so there is no switch yet for a hands-off Cilium cluster that does not want the policies. Ichor writes no standard `NetworkPolicy` objects (ADR 0103, open question 5).
