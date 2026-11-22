---
title: "ichor adopt"
description: "Bring an existing Talos cluster under Ichor's management with zero drift"
---

## ichor adopt

Bring an existing Talos cluster under Ichor's management with zero drift

### Synopsis

adopt brings an existing, already-running Talos cluster under Ichor's
management. For every TalosMachine already recorded for --cluster, it reads
the node's live configuration over the Talos API (using --talosconfig, the
same credential talosctl itself would use), renders a fresh configuration
from the TalosCluster/TalosMachine/TalosConfigPatch objects already in the
Kubernetes API (using --kubeconfig), and turns the difference into a new
TalosConfigPatch -- so that applying it on top of that fresh render
reproduces the node's live configuration byte for byte. adopt verifies this
itself before ever proposing a patch: if it cannot achieve zero drift for a
node automatically, it reports the differing fields and leaves that
node untouched rather than writing a patch that would not.

adopt runs in four phases, and nothing is applied to a node in any of them:

  1. Plan: reads the nodes and the Kubernetes side, detects the CNI, classifies
     the drift and prints the plan. --plan stops here and writes nothing.
  2. Consent: shows what will be written (and any foreign Cilium Helm release
     record that will be deleted after a backup) and asks; --yes skips the question.
  3. Write: creates the secrets bundle Secret, the TalosCluster and held
     TalosMachines (annotated as adopted), the initial TalosConfigPatch objects,
     and installs Ichor itself. A Cilium that Ichor does not own yet is then taken
     over as Ichor's Helm release.
  4. Release: lifts the hold one node at a time, control planes first, each
     waiting for Ready, so the first render is applied with zero drift.

Then, in the Ichor adopt installs, it turns Ichor's own sign-in on, as ichor init
does: it creates the first administrator (its generated password is printed once,
at the end), switches ichor-ui to sign-in, and sets spec.authentication.ichor on
the TalosCluster. That last step marks every control plane APIServerRestartRequired:
kube-apiserver restarts on each control plane, one at a time, only when you run
"ichor rollout config". The consent prompt names it. Sign-in cannot be turned off.
--ui-tls-secret, --ui-tls-cert and --ui-tls-key serve ichor-ui over your own certificate,
and --kube-api-server-url and --kube-api-tls-server-name set the Kubernetes API address in
users' kubeconfigs, as in ichor init (a Helm-managed Ichor is not touched, and takes these
as chart values).

adopt takes Talos v1.14 or later only: sign-in cannot be turned off, and the
kube-apiserver can trust Ichor's issuer only through the KubeAuthenticationConfig
that Talos v1.14 carries. Every node adopt reads, in a new cluster or an existing
one, must run Talos v1.14 or later (a node added with --add-machine to a cluster
adopted before needs only v1.12: its sign-in and issuer exist already), and a cluster Ichor does not manage yet gets the
version contract adopt probes from its first control plane (each minor from v1.14
up to the one it runs, keeping the one that differs least from the live
configuration, so a configuration generated for an older contract is refused
by name); a contract below v1.14 named with --version-contract is refused too. Either refusal says why and writes nothing. A cluster below v1.14 has to be
upgraded first, with talosctl or your usual tool.

The cluster's secrets are required: pass --secrets-bundle FILE, or
--recover-secrets with --recover-from ADDRESS to read them from a control
plane's machine config (not both).

A Cilium cluster is taken over after a diff check against the pinned chart;
--accept-cilium-diff accepts differences from the live objects and
--accept-cilium-changes the Ichor-owned settings that will change on the running
data plane (IPAM and the tunnel protocol are never changed). Any other CNI is
adopted only with --cni-unmanaged, which leaves the CNI exactly as it is: Ichor
will not install, upgrade or configure it. A cluster with two CNIs is refused.

--add-machine ADDRESS (repeatable) does not adopt a cluster: it adds a node to a
cluster Ichor already manages. The node already runs a configuration signed by
that cluster's CAs, for example because it joined with your own tooling after the
adoption or its TalosMachine was deleted. adopt reads it, plans it in the same three
classes, creates a held TalosMachine and releases the hold after the zero-drift check.
A node of another Talos cluster is refused; "ichor reclaim node" resets it. The
secrets bundle is read from the cluster's own Secret unless you pass one of the flags
above, and --talosconfig may come from "ichor create credentials". Reading the Secret needs get on it
in --ichor-namespace: bind the Role ichor-bundle-secret-role (chart value rbac.bundleSecret.names) with a
RoleBinding in that namespace; it grants get on the named Secrets and nothing else.

adopt only reads the Talos nodes (using --talosconfig); it never applies
configuration to them. Afterwards it advises a CA rotation, since adopted
clusters keep the old CAs, but never starts one.

```sh
ichor adopt [flags]
```

### Examples

```sh
  ichor adopt --cluster prod --secrets-bundle ./secrets.yaml --plan
  ichor adopt --cluster prod --secrets-bundle ./secrets.yaml
  ichor adopt --cluster prod --add-machine 192.168.10.24
```

### Options

```text
      --accept-cilium-changes             accept the Ichor-owned Cilium settings the takeover will change on the running data plane (never IPAM or the tunnel protocol)
      --accept-cilium-diff                accept the differences between the live Cilium objects and the pinned chart
      --add-machine stringArray           address of a node that already runs a configuration signed by this cluster's CAs but has no TalosMachine; adds it to the existing cluster instead of adopting the cluster (repeatable)
      --admin-user string                 name of the first administrator (default admin); its generated password is printed once to stderr and must be changed at the first sign-in
      --bundle-plaintext                  store the recovered secrets bundle unencrypted (default: encrypted with --bundle-passphrase-file, $ICHOR_BUNDLE_PASSPHRASE, or a generated passphrase)
      --cilium-values string              YAML file of Cilium Helm values for the Cilium adopt installs on a cluster with no CNI, or for a Cilium it takes over that has no Helm release record (Ichor-owned keys are refused)
      --cluster string                    name of the TalosCluster to adopt (required)
      --cni-unmanaged                     adopt the cluster with its CNI left exactly as it is: Ichor will not install, upgrade or configure it
      --context string                    context of the talosconfig to use (default: the file's selected context)
  -h, --help                              help for adopt
      --kube-api-server-url string        https URL of the Kubernetes API put in users' kubeconfigs (chart: ichorUI.users.kubeAPIServerURL)
      --kube-api-tls-server-name string   tls-server-name in users' kubeconfigs (chart: ichorUI.users.kubeAPITLSServerName)
  -n, --namespace string                  namespace the TalosCluster and its TalosMachines live in (default "ichor-system")
      --node string                       address of one control plane; adopt discovers the cluster's nodes from its membership when the TalosCluster does not exist yet
      --plan                              print the plan and write nothing
      --recover-from string               address of the control plane --recover-secrets reads the machine config from
      --recover-secrets                   rebuild the secrets bundle from a control plane's machine config (read-only) and store it under ~/.ichor/clusters/<cluster>/ with mode 0600
      --require-ichor                     fail when Ichor is not installed (for example with Helm) instead of installing it
      --secrets-bundle string             path to the cluster's existing secrets bundle (the file produced by 'talosctl gen secrets'); give this or --recover-secrets (not needed with --add-machine)
      --tunnel string                     tunnel protocol (vxlan or geneve) of the Cilium adopt installs on a cluster with no CNI; immutable afterwards
      --ui-tls-cert string                PEM certificate file for ichor-ui's TLS; adopt stores it with --ui-tls-key as a Secret
      --ui-tls-key string                 with --ui-tls-cert: PEM private key file (never printed)
      --ui-tls-secret string              serve ichor-ui over TLS with this existing kubernetes.io/tls Secret in ichor-system (chart: ichorUI.tls.secretName)
      --ui-url string                     the https URL at which browsers and the CLI reach ichor-ui, the issuer of every token; left out, the issuer is https://localhost:8443, where ichor ui reaches it
      --version-contract string           use this Talos version contract (v1.14 or later) for the TalosCluster adopt creates, instead of the probed one
      --wait-timeout duration             how long to wait for Ichor's admission webhooks before giving up (default 15m0s)
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor](ichor.md)	 - Manage a Talos Linux cluster through Ichor's CRDs

