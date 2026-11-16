---
sidebar_order: 9
---

# Add a machine that is already in the cluster

A machine that already runs a configuration signed by your cluster's own certificate
authorities, but that Ichor has no `TalosMachine` for, cannot be found by discovery (it
is not in maintenance mode) and must not be reset (a reset would wipe an etcd member or a
joined kubelet for no reason). This is the usual state of a node you added with your own
`talosctl apply-config` after the adoption, or of one whose `TalosMachine` you deleted by hand or removed without a wipe. A machine removed with `ichor delete node` (or the UI "Remove node") is sent the wipe and, once it answers in maintenance mode, shows up as a new pending machine instead. With `--reboot` the machine answers by itself, and the removal waits up to five minutes for that answer before it completes; if none comes, a Warning Event says the wipe is not confirmed. After the default power off the removal does not wait: power the machine on, and it shows up as a pending machine once it answers.
`ichor adopt --add-machine` registers it with the same safety as an adoption.

```sh
ichor create credentials prod                      # a talosconfig for the cluster, if you have none
ichor adopt --cluster prod --add-machine 10.0.0.21 --talosconfig ./talosconfig.ichor --plan
ichor adopt --cluster prod --add-machine 10.0.0.21 --talosconfig ./talosconfig.ichor
```

`--add-machine` is repeatable. It needs the `TalosCluster` to exist, in the namespace given
by `-n` (default `ichor-system`, where `ichor init` creates it, like every other command; unlike
a full adoption, this command does not check that `-n` is the namespace the controller runs in). The secrets bundle
is read from the cluster's own Secret (your kubeconfig must be allowed to read it), unless
you give `--secrets-bundle` or `--recover-secrets`; while a CA rotation is under way it
refuses. What it does, in order:

1. Reads each machine's identity and configuration with your talosconfig and checks that
   the configuration names this cluster's ID and Talos API CA. A machine that does not is
   refused with nothing written, and the message names `ichor reclaim node`.
2. Skips a machine whose UUID is already a `TalosMachine`, and refuses a control plane
   that would exceed `spec.controlPlane.count` (raise the count first: 1, 3 or 5).
3. Plans it like an adoption: what the typed documents can hold (sysctl, kernel modules,
   install disk, image) is captured as one machine patch, Ichor-owned settings are
   listed, and anything else blocks that machine with the paths and a hint. The role and
   the install disk come from the machine's own configuration.
4. After consent, creates the `TalosMachine` approved and held, then releases the hold
   only after a live zero-drift check, waiting for Ready. The controller applies the first
   render; the CLI applies nothing. No CA rotation advice is printed.

`--plan` writes nothing. The flags of an adoption that make no sense here (`--node`,
`--cni-unmanaged`, `--tunnel`, `--cilium-values`, `--accept-cilium-*`, `--require-ichor`,
`--version-contract`) are refused.

Not covered: the UI has no button for this yet; discovery still reports such an address as
"already configured" (and the `!` line of `ichor discovery add` now names both
commands).
