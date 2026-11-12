# 0126. FQDN rules for ichor-ui's external endpoint and no metrics scraping unless a namespace is named

Status: Accepted; built; hardware verification pending (FQDN rules and metrics policy) · Date: 2026-11-04 · Design section: Upgrades (Ichor), Security · Touches: 0100, 0105, 0108, 0114

Decided: the last `world` egress rule of Ichor's own policies, ichor-ui to its external OIDC issuer on 443, is replaced by an FQDN rule that the controller derives from the issuer the live ichor-ui Deployment is configured with (`--oidc-issuer` or `ICHOR_OIDC_ISSUER`) and writes as the CiliumNetworkPolicy `ichor-ui-oidc-egress`; the static install copy (`config/cilium-network-policy`, used by `ichor init` and the installer Job) therefore carries no `world` egress rule, and the chart renders one only as a fallback (`networkPolicy.cilium.dynamicTalosEgress=false` or `externalCIDRs`) and only while `ichorUI.oidc.enabled`; the controller's `/metrics` port is no longer open to the whole `cluster` entity: neither the static copy nor the chart opens it unless the chart's `networkPolicy.cilium.metricsNamespaces` names a namespace (optionally narrowed by pod labels, `metricsPodLabels`), and an installer or `ichor init` install allows a scraper with a policy of the operator's own, which the guide gives.

## Context

ADR 0108 deferred "FQDN network-policy rules and a metrics-namespace restriction" because the static install copy has no values to carry names. ADR 0100's amendment of 2026-11-02 has since moved most of what was `world` to controller-written policies: Talos nodes, cluster endpoints and S3 stores come from the CRs (`ichor-controller-talos-egress`), and the registries of the release check from the controller's flags (`ichor-controller-release-check`, ADR 0114). Both already use `toFQDNs` with the DNS proxy rule on kube-dns. What is left, read from the policies and the code:

| Left | Where | Why it is still wide |
|---|---|---|
| ichor-ui egress `world` 443 | static copy, chart | the external OIDC issuer; the pull request host of PR mode went away with PR mode. The issuer lives in a Deployment argument or environment variable, which the static copy cannot see. |
| controller ingress `cluster` 8443 | static copy, chart default | `/metrics`; any pod in the cluster can open a connection (the endpoint still needs a token the SubjectAccessReview accepts, ADR 0108 F15). |
| ichor-ui `/metrics` | serving port | shares the one HTTPS port with the UI and its Connect API, so no L4 rule can separate it. |

The controller reaches no registry, IdP or other external host in process apart from the S3 stores and the release-check registries, both already named by rule; the Cilium Helm Job and the kubelet's image pulls are outside these policies (ADR 0100).

## What the docs confirm about toFQDNs

Cilium, Layer 3 policy "DNS based" and Layer 7 "DNS Proxy" (`Documentation/security/policy/layer3.rst` and `layer7.rst`; the repository pins Cilium 1.20, `pkg/release/compat.go`, whose pages were read at 1.19, and the pages are the same):

- A `toFQDNs` rule works only with `--enable-l7-proxy=true` (Helm `l7Proxy`, default true; Ichor's own rendered Cilium config has `enable-l7-proxy: "true"`, `pkg/cni/cilium/testdata/bootstrap-*.golden.yaml`) and a rule that sends the endpoint's DNS through the proxy: an egress `toEndpoints` rule to `kube-system` `k8s-app=kube-dns` on port 53 with `rules.dns`. Without that rule the names never resolve to allowed addresses and every dial fails. Ichor's static and chart policies carry the rule (`matchPattern: "*"`) since ADR 0100's amendment.
- `toFQDNs` cannot share a rule with another L3 selector (`toEndpoints`, `toCIDR`); each kind is its own rule, as `buildEgress` already writes them. It may carry `toPorts`.
- The DNS proxy trusts the cluster DNS: the docs advise intercepting only the cluster resolver, which is what the rule does.
- Policies add up, so a `world` rule in a static policy would cancel any narrowing done elsewhere. That is why the static copy loses its `world` rule rather than the dynamic policy merely adding to it.
- An in-cluster issuer (`keycloak.sso.svc`) cannot be matched by name: the ClusterIP is translated to a backend before policy. `toServices` with `k8sService` is used for it, as for an in-cluster S3 store (ADR 0100); an IP-literal issuer becomes a `toCIDRSet`.

On a cluster Ichor adopts without managing Cilium (`cni.preset: unmanaged`, or an existing Cilium it only detects), `enable-l7-proxy` is the operator's setting. Ichor does not change it (restrict Ichor, not the user's cluster). With the proxy off, the new rule and the earlier S3 and release-check rules cannot match: the OIDC login fails with a timeout, and the log of ichor-ui shows the dial refused. The escape hatches stay: `networkPolicy.cilium.dynamicTalosEgress=false` (chart) renders the old `world` rules and stops the controller from writing the policies, and `networkPolicy.cilium.enabled=false` removes Ichor's policies altogether.

## Decision

1. **Where the names come from: the object that uses them.** The controller reads the ichor-ui Deployment(s) in its namespace (label `control-plane=ui-backend`; the Role already has `get;list` on Deployments), takes the issuer from the container `ichor-ui` (an `--oidc-issuer=` argument wins over `ICHOR_OIDC_ISSUER`, as the flag does in `cmd/ichor-ui`), and writes `ichor-ui-oidc-egress`: the ichor-ui pods to that host on the URL's port (443 and 80 by scheme). No issuer, no policy (the controller deletes a stale one). It uses the same helpers as the S3 rule (`toFQDNs`, `toServices` for `svc.ns.svc`, `toCIDRSet` for an IP).
   - Works the same for the installer path, `ichor init`, and the chart: the chart no longer repeats the issuer in a second place, so the policy cannot drift from what ichor-ui actually dials.
   - Written only beside Ichor's installed `controller-manager` policy (a lone egress rule would cut every other flow), disabled by `--no-cilium-policies` and `--talos-egress-policy=false`, like the other two policies. Requeued every 30 seconds; the controller has no watch on Deployments (its Role grants no `watch`). A newly configured issuer is therefore reachable within about a minute; ichor-ui retries its provider setup.
   - The policy selects the ichor-ui pods only. The shared `ichor-controller-talos-egress` selects both workloads and must not carry an IdP the controller never dials.
2. **The static copy and the chart.** `ui` loses `world` 443 in `config/cilium-network-policy`. The chart renders an external rule for ichor-ui only when `ichorUI.oidc.enabled` and either `dynamicTalosEgress` is false (then `world` or `externalCIDRs` on `externalPorts`, as before) or `externalCIDRs` is set (a fallback next to the dynamic policy). A chart install with OIDC off renders no external egress for ichor-ui at all. `ichor.cnp.world` stays for the controller's own fallback.
3. **Metrics.** `controller-manager` loses the `cluster` rule on the metrics port in the static copy, and the chart renders the rule only when `networkPolicy.cilium.metricsNamespaces` is not empty: `fromEndpoints` for each namespace (`k8s:io.kubernetes.pod.namespace`), plus, when `metricsPodLabels` is set, those labels (`k8s:<key>`) on every listed namespace, so that only the Prometheus pods, not the namespace's other pods, can connect. The default is empty, no scraping. A chart install with metrics enabled and no namespace named gets a NOTES.txt notice. An installer or `ichor init` install has no value to carry the namespace; the operator adds a CiliumNetworkPolicy of their own for the metrics port, which `docs/guides/install-with-helm.md` (section "Network policies") gives as a ready example. Policies add up, so it also works with the chart.
4. **ichor-ui's `/metrics` is not restricted by network policy.** It is the serving port; an L4 rule cannot tell the UI from the scrape, and L7 inspection is not possible on a TLS port Cilium does not terminate. The restriction there is the token check (a caller needs `get` on the non-resource URL `/metrics`, ADR 0108 F15). `uiIngressCIDRs` still narrows who reaches the port from outside the cluster. Stated, not fixed.

## Alternatives rejected

- **A new `IchorInstallation.spec.network` (metrics namespaces, extra egress hosts).** It is the place the project keeps runtime settings (ADR 0105), but a field needs the effective-status mirror, the settings gate in the admission webhook, the proto messages, the UI and the CLI; for a value an operator sets once and that only a policy reads, that surface is out of proportion, and for the issuer it would duplicate a value that already exists and could disagree with it. Appending the field later stays possible (CRD fields are append-only).
- **The installer customizes the `ui` policy** from the settings it reads back from the live Deployments (`ReadUISettings`), as it does for `--ui-url`. That only updates on an upgrade, so an OIDC issuer added to the Deployment afterwards would be blocked until the next upgrade, and every issuer change would be a manual Ichor upgrade.
- **Keep `world` and only narrow the port.** Unchanged exposure to any host on 443 from a pod that holds the impersonation right.
- **A shared dynamic policy for the issuer.** The existing one selects both workloads; adding the issuer to it would open the IdP to the controller.
- **Metrics from `cluster` by default, namespaces optional (the old chart default).** Any pod in the cluster could open the port. The endpoint is authenticated, but least privilege says closed until named; v0.1.0 has no installed users to break.
- **A controller-maintained metrics policy fed by a flag.** The installer would overwrite the flag on the next upgrade, so a flag cannot be the operator's persistent setting.

## Consequences

- An install with an external OIDC issuer needs the controller running and the L7 proxy on for the login to reach the IdP, within about a minute of the setting. Without the proxy the chart's escape hatch above is the way back.
- Nothing in `ichor-system` can scrape the controller until the operator names a source. A Prometheus that worked before an upgrade stops until the namespace is named (the chart NOTES and the guide say so); the controller's own ServiceMonitor is unchanged.
- The static policies have no `world` egress rule left: every egress allow is the Kubernetes API, this cluster's nodes, DNS, or a name or address that an Ichor object or the Deployment names.

## Not verified (hardware, after integration)

On a Talos cluster with Cilium (the outer cluster, `ichor-e2e` only), `hubble observe --verdict DROPPED -n ichor-e2e-ichor` or `cilium-dbg monitor --type drop` on the node of the pod:

1. Set an external issuer on the ichor-ui Deployment (env `ICHOR_OIDC_ISSUER=https://<a host with a name>`): within about a minute `kubectl get ciliumnetworkpolicy ichor-ui-oidc-egress` exists with one `toFQDNs` rule on 443; ichor-ui logs the provider as reachable; `curl` from the ichor-ui pod's network namespace (a debug pod with the ichor-ui labels in the same namespace) to that host succeeds and to another host (`https://example.com`) is dropped.
2. Remove the issuer: the policy is deleted and the issuer host is dropped.
3. `cilium-dbg fqdn cache list` on the pod's node shows the issuer name with its addresses; `cilium-dbg policy get` shows no `world` egress rule for the ichor-ui endpoint.
4. An in-cluster issuer (`http://dex.dex.svc:5556`) is allowed through `toServices`.
5. Metrics: from a pod in another namespace, `curl -k https://<controller pod>:8443/metrics` times out with the static copy; with a user-written policy naming that namespace it answers 401 without a token and 200 with a token that may `get` `/metrics`; from a third namespace it still times out. With the chart: the same with `metricsNamespaces={monitoring}` and `metricsPodLabels={app.kubernetes.io/name: prometheus}`, a pod of the namespace without the label timing out.
6. The earlier rules still work: the S3 backup, the release check, discovery, the UI behind its LoadBalancer or NodePort, the webhook across nodes.
7. With `enable-l7-proxy` turned off in a test cluster's `cilium-config` and the agents restarted, the issuer is unreachable (the documented limit), and `dynamicTalosEgress=false` restores it.

## As built

- Controller: `internal/controller/ui_egress.go` (`UIEgress`, a leader-only runnable added in `cmd/main.go`, 30 seconds), `talos_egress.go` (`urlTarget.rule()` is the shared per-target rule, `parseURLTarget` replaces `parseS3Endpoint`). Tests: `ui_egress_test.go` (issuer parsing, the spec, and an envtest spec for gating, writing, no rewrite when unchanged, removal with the issuer, disabled).
- Static copy and chart: `config/cilium-network-policy/policies.yaml`, `dist/chart/templates/network-policy/cilium.yaml`, `values.yaml` (`metricsPodLabels`, new comments), `NOTES.txt`. Tests: `dist/chart/netpol_test.go` and `netpol_egress_test.go` (chart equals the static copy; no `world` egress for ichor-ui; the external rule only as a fallback and only with OIDC on; the metrics port closed by default; namespaces and pod labels).
- Docs: `docs/guides/install-with-helm.md` "Network policies", `docs/design/architecture.md`.
- No CRD, RBAC, proto, UI or CLI change: the Role already grants `get;list` on Deployments in the namespace.
