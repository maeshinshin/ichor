# 0009. Reboot necessity is predicted, because Talos will not answer it

Status: Accepted · Date: 2026-10-25 · Design section: 5.6 (configuration reconcile)

Decided: Keep predicting reboot necessity from `pkg/fieldmeta`'s field-path table; Talos itself won't answer the question

## Context

`pkg/fieldmeta` predicts, from a small table keyed by field path, whether a
configuration change needs a node reboot. It defaults to reboot-required and
carries eight hand-written classifications. Its own author flagged the obvious
weakness: those eight are Talos domain knowledge, so a machinery bump cannot
tell us when Talos quietly changes the behaviour underneath them.

Reading machinery v1.14.3's API suggested the prediction might be unnecessary.
`ApplyConfigurationRequest.Mode` has `AUTO`, with `REBOOT` deprecated in favour
of it; `DryRun` is a bool on the same request; and the response carries `Mode`
("States which mode was actually chosen"), `ModeDetails` and `Warnings`. That
reads exactly like Talos offering to answer the question itself, per node and
per version, which would be strictly better than predicting it.

Talos does not answer it.

**`AUTO` is a hardcoded alias for `NO_REBOOT`.** From Talos v1.14.3,
`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go`:

```go
case machine.ApplyConfigurationRequest_AUTO:
    in.Mode = machine.ApplyConfigurationRequest_NO_REBOOT
    fallthrough
```

Nothing in that RPC ever sets `Mode = REBOOT`, and the switch never looks at
the diff. `ModeDetails` is a static string. Three dry runs against a node — one
changing only a DNS nameserver, one changing only the installer image tag, one
applying a whole worker configuration — return byte-identical `Mode` and
`ModeDetails`.

The real decision lives elsewhere. Each subsystem's own controller reacts to
the already-written configuration asynchronously and asks for a reboot through
a `RebootRequest` resource, which a separate controller consumes. There is no
point in the API where Talos summarises that into "this change needs a
reboot".

## Decision

Keep `pkg/fieldmeta`, and keep predicting. Its table is the only mechanism that
can answer the question where Ichor needs it answered.

Two places need it, and neither can dry-run:

- **A validating webhook on `TalosConfigPatch`, at admission time.** There may
  be no node to ask: the cluster may not be bootstrapped, the target machine
  may not exist, and admission has to answer synchronously anyway.
- **The reconciler, before it applies.** Design 5.6 step 4 requires taking the
  cluster lock, running the node-operation hook and draining *before* a
  reboot-causing apply. Learning the answer from Talos after applying is too
  late by construction.

So the design's wording — the mode is derived from the diff — stands.

Two further properties of the apply API are worth acting on separately:

- **`Warnings` on the apply response is genuinely useful**, just not for this.
  It comes from `ValidateAtRuntime` and surfaces deprecated-field migration
  hints, missing cross-document configuration (etcd encryption on a control
  plane, a cluster ID or secret missing when discovery is on) and
  `KubeNodeConfig` label mismatches. The reconciler should surface it in status
  rather than dropping it.
- **Dry run is genuinely side-effect free.** A node's `MachineStatus` version
  and timestamp are unchanged after dry runs, not even rewritten with the same
  value. So it is safe to use for the
  impact preview's *validation* half, even though it cannot answer the reboot
  half.

## Consequences

- The eight hand-written entries stay, with the risk their author named: a
  Talos behaviour change will not announce itself. The mitigation is the
  default — anything not in the table is reboot-required — so the failure mode
  of a stale table is an unnecessary reboot, not a node left running a
  configuration it does not have.
- Whenever the pinned Talos version moves, those eight are worth re-reading
  against the release notes. That is a cheap, bounded review, and better than
  the alternative of trusting an API that does not answer.
- The reconciler should pass `DryRun` first for validation and warnings, then
  apply for real — two calls, with the mode decided by the table in between.

## Addendum: multi-document and registry paths

The table originally knew only the legacy `Config` paths and two standalone
kinds, so every other path fell to reboot-required. Ichor's own render hit
that: adding `TalosCluster.spec.registries` was refused with `RebootRequired`.
In the v1.14.3 source, what Talos applies live is separated from what it does
not by the controller that consumes the field. A rule exists only where that
consumer is identified; the default is unchanged.

Ichor's default render at the v1.14 contract (control plane and worker) is the
legacy `Config` document (`machine.*`, `cluster.*`) plus the standalone kinds
DiscoveryServiceConfig, DiscoveryIdentityConfig, ResolverConfig, VolumeConfig
(EPHEMERAL), FilesystemTrimConfig, SecurityProfileConfig, HostnameConfig,
KubeClusterConfig, KubeNodeConfig, KubeletConfig, KubeNetworkConfig,
KubeAPIServerCAConfig, KubePrismConfig, and on control planes also
KubeAggregatorCAConfig, KubeAdmissionControlConfig, KubeAuditPolicyConfig,
KubeAuthenticationConfig, KubeAuthorizerConfig, KubeEtcdEncryptionConfig,
KubeServiceAccountConfig, KubeAPIServerConfig, KubeControllerManagerConfig,
KubeSchedulerConfig, KubeProxyConfig, KubeFlannelCNIConfig, KubeCoreDNSConfig.
Patches add SysctlConfig, KernelModuleConfig, VolumeConfig, UserVolumeConfig,
RawVolumeConfig and legacy `Config` fragments. Registries are injected into the
legacy `machine.registries`, not into a `RegistryMirrorConfig` document, so the
path that mattered was `machine.registries.mirrors.<host>.endpoints`.

Mode handling, for reference: `ApplyConfiguration` in
`internal/app/machined/internal/server/v1alpha1/v1alpha1_server.go` writes the
config in `NO_REBOOT` and never inspects the diff; whether anything needs a
reboot is decided by the controllers that consume the resource. Paths below are
relative to the `siderolabs/talos` module at v1.14.3 (`pkg/machinery/...` is the
machinery module).

| Document and field | No reboot? | Where in the source |
| --- | --- | --- |
| `machine.registries.mirrors` | yes | `internal/app/machined/pkg/controllers/cri/registries_config.go` builds `RegistriesConfig` from the config on every change; `internal/app/machined/pkg/controllers/files/cri_config.go` renders it to containerd's `hosts` directory via `internal/pkg/containers/cri/containerd/hosts.go` (`GenerateHosts`), which containerd reads per pull |
| `machine.registries.config` (auth and TLS), `RegistryAuthConfig`, `RegistryTLSConfig`, `RegistryMirrorConfig` | yes (CRI restarts itself; see below) | `controllers/cri/registries_config.go` lines 82-125 fold auth, mirrors and TLS from legacy and document forms into one `RegistriesConfig`; `containerd.GenerateCRIConfig` (`internal/pkg/containers/cri/containerd/containerd.go`:24-50) writes auth into the CRI config file; `controllers/files/cri_config.go`:139-180 re-renders that file on every change; `controllers/cri/service.go`:84-112 compares the file's `SpecVersion` and restarts the CRI service when it moved |
| `KubeNodeConfig.labels`, `.annotations`, `.taints`; `machine.nodeLabels`, `.nodeAnnotations`, `.nodeTaints` | yes | `controllers/k8s/node_label_spec.go`, `node_annotation_spec.go`, `node_taint_spec.go` write specs from `K8sNodeConfig()`; `node_apply.go` applies them to the Node and removes what it owned. `pkg/machinery/config/types/v1alpha1/v1alpha1_k8s_bridge.go` (`k8sNodeConfigShim`) maps the legacy fields onto the same interface |
| `cluster.allowSchedulingOnControlPlanes`, `.allowSchedulingOnMasters` | yes | read only by the taint shim above (`v1alpha1_clusterconfig.go` `ScheduleOnControlPlanes`, used only in `v1alpha1_k8s_bridge.go`) |
| `KubeAPIServerConfig`, `KubeControllerManagerConfig`, `KubeSchedulerConfig`, except `.image` | yes | `controllers/k8s/control_plane.go` reads `K8sAPIServerConfig()` etc. and regenerates the static pod; same consumer as the legacy `cluster.apiServer` rules |
| the same three kinds, `.image` | no, on purpose | see below |
| `KubeProxyConfig`, `KubeletConfig` (including `.image`) | unknown, kept default | see below |

(`controllers/` is `internal/app/machined/pkg/controllers/`.)

**The Kubernetes component images are deliberately not no-reboot.**
`KubeAPIServerConfig.image`, `KubeControllerManagerConfig.image`,
`KubeSchedulerConfig.image`, `KubeProxyConfig.image` and `KubeletConfig.image`
would apply live in Talos, but a safe rule makes the machine controller apply
all nodes the moment `spec.kubernetesVersion` changes, which breaks the
one-node-at-a-time order ADR 0057's executor provides (it applies the image
fields itself with an explicit no-reboot apply). The explicit `.image` entries
in `pkg/fieldmeta` carve them out of their document's rule, and a test pins
all five. The legacy `cluster.apiServer.image`, `cluster.controllerManager.image`
and `cluster.scheduler.image` are still covered by the older `cluster.*` safe
rules, so on a pre-1.14 contract the same hazard exists. That is a separate
decision and is not made here.

**Left at the default, because the consuming behaviour is not established:** `KubeProxyConfig` (the open
question about the kube-proxy DaemonSet stands), `KubeletConfig` fields other
than the image and `machine.kubelet.*` (kubelet restart behaviour not traced),
`KubeNodeConfig.nodeIP`, `ResolverConfig.hostDNS`, `HostnameConfig`,
`DiscoveryServiceConfig`/`DiscoveryIdentityConfig`, `SecurityProfileConfig`,
`KubeClusterConfig`, `KubeNetworkConfig`, the CA, admission, audit,
authentication, authorizer, etcd-encryption, service-account, Prism, Flannel
and CoreDNS kinds, `VolumeConfig`/`FilesystemTrimConfig` beyond
`provisioning`. Ichor's inputs never vary most of these after the first
apply.

## Addendum: an unchanged hostname needs no reboot

fieldmeta stays path-only, so `HostnameConfig.*` and `machine.network.hostname` are reboot-required. `assessChange` (`internal/controller/hostname_noop.go`) drops those paths when `spec.hostname` equals the node's current Nodename, so the release that began rendering `spec.hostname` does not demand a reboot of every existing node; a real rename stays reboot-required and its message says the Kubernetes node keeps its old name until the reboot.

## Addendum: registry auth and TLS apply without a reboot

Machinery's doc comment on `RegistryConfig.RegistryAuth` (`pkg/machinery/config/types/v1alpha1/v1alpha1_types.go`:2058) says "changes to the registry auth will not be picked up by the CRI containerd plugin without a reboot", which would make registry auth reboot-required. That comment is stale in the v1.14.3 machined source: auth is written into the CRI config file (`GenerateCRIConfig`, `containerd.go`:24-50), which containerd reads only at start; but `ServiceController.reconcile` (`internal/app/machined/pkg/controllers/cri/service.go`:84-112) restarts the CRI service whenever the `SpecVersion` of that file (or the base runtime spec) changes, and `CRIConfigController` (`controllers/files/cri_config.go`:139-180) re-renders it on every registries change. Running pods keep running while containerd restarts. TLS and mirrors become hosts.toml files and need no restart at all. So `machine.registries.config`, `RegistryAuthConfig`, `RegistryTLSConfig` and `RegistryMirrorConfig` are safe (the legacy host key contains dots, so the rule is the whole subtree). This describes Talos v1.14.3; Ichor targets v1.14.
