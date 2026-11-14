---
sidebar_order: 10
---

# Bootstrap

```sh
ichor init --preset standard [--cni-tunnel vxlan|geneve] [--ui-url URL | --allow-unauthenticated]
```

1. Generate the secrets bundle locally; nag the user to back it up.
2. Settle the shape of the cluster: topology preset, endpoint (a VIP is
   recommended even for one node, because changing the endpoint later means
   re-issuing certificates), disk roles for the first node.
3. Apply the rendered config to the first control plane. Its inline manifest
   carries only Cilium, the one component Kubernetes cannot start without: the
   chart pinned in the binary, rendered with Helm's Go SDK, no network at init
   time and the same bytes every run. Everything else is applied
   later through the API server, with every write retried for a few minutes on
   a transient error.
4. Bootstrap etcd, fetch the kubeconfig, store the bundle as a Secret, apply
   Ichor's install manifest.
5. Create the CRs and the `IchorInstallation`, then apply
   Ichor's NetworkPolicies once Cilium is Ready.
6. Wait for the `CNIHandover` condition on the `TalosCluster`. `init` shows its
   steps as a short list with a spinner on a terminal (one line per change in a
   pipe or a log), and the last reason when the wait runs out; the controller
   keeps working either way.

There are no flags to choose a CNI.
Adding later nodes goes through the normal approval flow; `init` is the
special case of approving the first control plane.

`ichor adopt` brings a running Talos cluster that Ichor did not create under
management. It needs Talos v1.12 or later on every node it adopts
and refuses an older or mixed cluster with "upgrade Talos first", writing
nothing. Above that floor, a cluster is adopted as it runs, whatever its version against this Ichor's set: a version
the compatibility table does not know at all is refused, and an incompatible pair is reported and adopted. It works in four phases, and nothing is applied to a node until the
last one:

1. Plan (`--plan` stops here and writes nothing). Read-only inspection with
   the operator's own talosconfig: Talos and Kubernetes versions, the CNI and
   where it came from, the kube-proxy setting, KubePrism, inline manifests
   and each approved machine's live config. The `versionContract` is probed
   from the running version down to v1.12 and the one with the fewest
   unrepresentable paths is chosen (`--version-contract` overrides it).
2. Write, after consent. The bundle Secret, the `TalosCluster` (annotation
   `ichor.maesh.dev/adopted`), the `TalosMachine`s (identity read over the
   authenticated API), the `TalosConfigPatch`es and, if Ichor is not running,
   Ichor's install manifest. Every `TalosMachine` carries the annotation
   `ichor.maesh.dev/hold-apply`: the reconciler computes and reports drift
   (`AdoptionHeld`) and never applies.
3. Cilium takeover (see [Cilium](cilium.md)), skipped for an unmanaged CNI. A cluster
   with no CNI at all is different: its nodes are NotReady, so
   Ichor's own pods could not start. Adopt then installs Ichor's pinned Cilium
   itself, in process, as Ichor's own Helm release, and waits for the nodes to
   be Ready before it installs Ichor or writes any CR; the order is Cilium,
   Ichor, CRs, hand-over, release. The cluster is adopted as the managed
   `cilium` preset and `--cni-unmanaged` is refused there. This needs KubePrism
   on port 7445 on every node and a pod CIDR on every node.
4. Release of the hold, one machine at a time, control planes first, only
   after a live zero-drift check passes. The controller then applies the
   first render under the cluster lock like any rollout.

The old "zero drift or nothing" rule now sorts differences into three classes.
Class A is representable: sysctl and kernel-module documents, and values that
have spec fields (subnets, install disk); adopt stores them as
patches. Class B is Ichor-owned (the Cilium inline entry, KubePrism,
kube-proxy when replaced, removal of Talos's flannel); the first render
changes them, so they are listed and need consent, and they do not block.
Class C is everything else, including other inline or extra manifests; it
blocks that machine, with the path list and a hint, and adopt leaves it
untouched.

Secrets. `--secrets-bundle FILE` takes a `talosctl gen secrets` file. With
`--recover-secrets` (and `--recover-from ADDRESS`, a control plane) adopt
rebuilds the bundle from that node's machine config, read-only, and stores it
under `~/.ichor/clusters/<name>/` with mode 0600. A wrong or partial bundle
cannot get through, because it shows up as secret-field drift and no patch is
produced. After adoption the controller uses a certificate minted from the
bundle. The previous administrators' client certificates stay valid, so adopt
prints, and the UI shows as a dismissible notice (condition
`CARotationAdvised`), the advice to run `ichor rotate ca`. Nothing
starts a rotation.

Ichor can also be installed with Helm: `helm install` of the chart
in `dist/chart` (published as the Helm repository `https://ichor.maesh.dev/charts` and as
`oci://ghcr.io/maeshinshin/charts/ichor` once a release tag is pushed) installs Ichor, not a cluster. `ichor adopt` then
detects the Helm release, skips its own install step, never creates an
`IchorInstallation`, requires Ichor's version to equal the CLI's, and registers
the cluster. A Helm-installed Ichor is upgraded with `helm upgrade`. The chart
refuses to install on a cluster with no pod network (`preflight.cni`); that one
case is covered by `ichor bootstrap cni`, which runs the same Cilium step as
adopt. The chart never ships Cilium. See `docs/guides/install-with-helm.md`.
