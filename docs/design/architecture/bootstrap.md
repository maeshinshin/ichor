---
sidebar_group: "Cluster lifecycle"
sidebar_order: 10
---

# Bootstrap

![Two ways in. ichor init generates the secrets bundle, settles the shape, applies the config to control plane 1 with only Cilium inline, bootstraps etcd, creates the CRs and waits for CNIHandover. ichor adopt plans read-only, writes after consent with every machine held, takes over Cilium and releases machines one at a time after a zero-drift check. Adopt sorts each difference into class A, stored as patches; class B, listed and needing consent; and class C, which blocks the machine.](../../images/bootstrap.light.drawio.svg)

*`init` creates a cluster; `adopt` brings a running one under management, and nothing is applied to a node until its release.*

## `ichor init`

```sh
ichor init --preset standard [--cni-tunnel vxlan|geneve] [--ui-url URL]
```

- Generate the secrets bundle locally and nag the user to back it up.
- Settle the shape. Use a VIP as the endpoint even for one node: changing it
  later means re-issuing certificates.
- The first control plane's inline manifest carries only Cilium, the one
  component Kubernetes cannot start without. The chart is pinned in the binary
  and rendered with Helm's Go SDK, so there is no network at init time and the
  same bytes every run. Everything else goes through the API server, each write
  retried for a few minutes on a transient error.
- Bootstrap etcd, fetch the kubeconfig, store the bundle as a Secret, apply
  Ichor's install manifest, create the CRs and the `IchorInstallation`, and
  apply Ichor's NetworkPolicies once Cilium is Ready.
- Wait for the `CNIHandover` condition on the `TalosCluster`. On a terminal
  `init` shows a short step list with a spinner (one line per change in a pipe
  or log) and the last reason if the wait runs out. The controller keeps working
  either way.

There is no flag to choose a CNI. Later nodes go through the normal approval
flow; `init` is the special case of approving the first control plane.

## `ichor adopt`

Brings a running Talos cluster Ichor did not create under management.

**Floor.** Talos v1.14 and later only, because sign-in is always on and only
v1.14 can carry Ichor's issuer. A node below it is refused with "upgrade Talos
first", writing nothing, and so is a new `TalosCluster` whose version contract
is below v1.14. A node added with `--add-machine` to a cluster adopted earlier
passes a v1.12 floor instead, since that cluster's sign-in and issuer exist. Above the floor a cluster is adopted as it
runs: a version the compatibility table does not know is refused; an
incompatible pair is reported and adopted.

**Contract probe.** On the first control plane alone, from its running version
down to v1.12 (below the floor so an older configuration is refused by name).
The contract with the fewest unrepresentable paths wins; `--version-contract
v1.14` is the way out when an older one wins on a v1.14 control plane.

Four phases; nothing is applied to a node until the last.

1. **Plan** (`--plan` stops here and writes nothing). Read-only inspection with
   the operator's talosconfig: Talos and Kubernetes versions, the CNI and its
   origin, the kube-proxy setting, KubePrism, inline manifests and each approved
   machine's live config.
2. **Write**, after consent: the bundle Secret, the `TalosCluster` (annotation
   `ichor.maesh.dev/adopted`), the `TalosMachine`s (identity read over the
   authenticated API), the `TalosConfigPatch`es and, if Ichor is not installed or its install is partial, its
   install manifest. A running Ichor of another version than the CLI is refused
   with nothing written; `ichor upgrade ichor` moves it. Every `TalosMachine` carries
   `ichor.maesh.dev/hold-apply`: the reconciler computes and reports drift
   (`AdoptionHeld`) and never applies.
3. **Cilium takeover** (see [Cilium](cilium.md)), skipped for an unmanaged CNI.
   A cluster with no CNI has NotReady nodes, so Ichor's pods could not start:
   adopt installs Ichor's pinned Cilium itself, in process, as Ichor's own Helm
   release, and waits for Ready nodes before installing Ichor or writing any CR.
   Order: Cilium, Ichor, CRs, hand-over, release. The cluster is adopted as the
   managed `cilium` preset and `--cni-unmanaged` is refused. It needs KubePrism
   on port 7445 and a pod CIDR on every node.
4. **Release** of the hold, one machine at a time, control planes first, only
   after a live zero-drift check passes. The controller then applies the first
   render under the cluster lock like any rollout.

### Differences

| Class | Holds | Result |
|---|---|---|
| A, representable | sysctl and kernel-module documents; values with spec fields (subnets, install disk) | stored as patches |
| B, Ichor-owned | the Cilium inline entry, KubePrism, kube-proxy when replaced, removal of Talos's flannel | listed; need consent; do not block |
| C, everything else | including other inline or extra manifests | blocks that machine, with the path list and a hint; left untouched |

### Secrets

`--secrets-bundle FILE` takes a `talosctl gen secrets` file. With
`--recover-secrets` (and `--recover-from ADDRESS`, a control plane) adopt rebuilds
the bundle read-only from that node's machine config and stores it under
`~/.ichor/clusters/<name>/` (0600). A wrong or partial bundle cannot get through:
it shows as secret-field drift and no patch is produced.

After adoption the controller uses a certificate minted from the bundle. The
previous administrators' client certificates stay valid, so adopt prints, and the
UI shows as a dismissible notice (condition `CARotationAdvised`), the advice to
run `ichor rotate ca`. Nothing starts a rotation.

## Installing with Helm

`helm install` of the chart in `dist/chart` (Helm repository
`https://ichor.maesh.dev/charts`, or `oci://ghcr.io/maeshinshin/charts/ichor`
once a release tag is pushed) installs Ichor, not a cluster. `ichor adopt` then
detects the release, skips its install step, never creates an
`IchorInstallation`, requires Ichor's version to equal the CLI's, and registers
the cluster. Upgrade with `helm upgrade`.

The chart never ships Cilium and refuses a cluster with no pod network
(`preflight.cni`); `ichor bootstrap cni` covers that case with the same Cilium
step as adopt. See `docs/guides/install-with-helm.md`.
