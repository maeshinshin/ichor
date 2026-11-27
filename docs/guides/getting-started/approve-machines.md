---
sidebar_order: 3
---

# 3. Approve machines

`ichor init` registers the first node. Other nodes are found in maintenance
mode by scanning addresses you give (`init` takes `--discover-address` and
`--discover-cidr` too) and wait as pending until you approve them:

![Five steps from left to right: discovery finds the node in maintenance mode, it waits as a pending machine until you approve it, you approve it, the controller applies the configuration while the node installs Talos and reboots, and the node joins as a control plane or a worker. A machine with a weak identity is refused, instead of approved.](../../images/approve-machine-path.light.drawio.svg)

*From a node in maintenance mode to a cluster member.*

```sh
ichor discovery add 192.168.1.51 --cluster demo
ichor get nodes --cluster demo
ichor approve node NAME --role worker --install-disk /dev/sda
```

`NAME` is the pending machine from the list: the first eight characters of its
hardware UUID while unique (or the full UUID); an ambiguous short name is
refused with the candidates. Other commands accept it too.

The command asks to confirm unless `--yes`, then prints `✓ approved fa70962a as
worker in demo` (`-o yaml` prints the machine). A `!` line after it is advice, not
a refusal: the disk, disk role or NIC was not among those discovery recorded.
`approve` also takes a hostname, labels, a static address and extra disk roles
(`ichor approve node --help`). A machine with no UUID, serial or permanent MAC is refused.

A node approved without `--install-disk` and with no disk in its configuration
stays `Ready=False/InstallDiskUnknown`, and nothing is applied to it. `ichor approve node`
refuses a node that is already approved, so set the disk with
`ichor set install-disk NODE --install-disk DISK` (optionally `--install-image REF`), or in the UI
(the machine's install disk). It only works until the machine has an applied configuration.

After approval the controller renders and applies the configuration; the node
installs Talos, reboots and joins.

> [!NOTE]
> The `single` preset has nothing to approve: skip this step. With `compact-ha`, approve
> the two further control planes with `--role controlplane`, as for `standard`. With `standard`,
> approve the two further control planes first with `--role controlplane` (see
> [Virtual IP](create-the-cluster/vip.md#flags)), then workers.

To turn a pending machine away, `ichor reject node NAME`. It is recorded, never
deleted, and approving it later reverses it. `spec.approvalState` (`pending`,
`approved`, `rejected`) is the status; `spec.approvalDecision` records who
decided (`actor`) and when (`at`).

An operation that waits for approval (for example an upgrade, which reboots each
node in turn) is started with `ichor approve operation NAME`.

## The sign-in line

Sign-in is always on, so the CLI prints this on stderr until you log in:

```text
ichor: ichor-ui requires OIDC and there is no usable ichor-ui login; using your own kubeconfig and RBAC in this process instead (to use ichor-ui, run `ichor auth login --issuer https://localhost:8443 --audience ichor-ui`)
```

The command still works: it runs in this process with your kubeconfig and
Kubernetes permissions, as do the later steps. To go through ichor-ui and its
users, log in once. The issuer is your `--ui-url`, or `https://localhost:8443`
(`ichor ui` must be running):

```sh
ichor auth login --issuer https://192.168.1.60 --audience ichor-ui
```

The login opens your browser, so the URL must be reachable from this machine. If
ichor-ui uses a certificate from the Ichor serving CA, which your machine does
not trust, export that CA and pass it:

```sh
kubectl -n ichor-system get configmap ichor-ui-ca -o jsonpath='{.data.ca\.crt}' > ichor-ca.crt
ichor auth login --issuer https://192.168.1.60 --audience ichor-ui --ca-file ichor-ca.crt
```

The warning stops once the login is cached. See [Users and login](../users-and-login.md).
