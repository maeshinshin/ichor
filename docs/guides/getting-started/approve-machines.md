---
sidebar_order: 3
---

# 3. Approve machines

`ichor init` registers the first node itself. Other nodes are found in
maintenance mode by scanning addresses you give, and wait as pending machines
until you approve them. Point the scan at them (`init` takes
`--discover-address` and `--discover-cidr` too):

```sh
ichor discovery add 192.168.1.51 --cluster demo
ichor get nodes --cluster demo
ichor approve node NAME --role worker --install-disk /dev/sda
```

`NAME` is the pending machine from the list. A machine is named after its
hardware UUID, so the list shows the first eight characters of it, as long as
those are unique; pass that short name as it is, or the full UUID. If a short
name fits more than one machine, the command refuses and lists the candidates.
The other commands that take a machine name accept the short name too. The
command asks for confirmation unless `--yes`, then prints one line (`-o yaml` prints the
machine as before):

```text
✓ approved fa70962a as worker in demo
```

If the install disk, a disk role or the NIC you named was not among those
discovery recorded on the node, a `!` line follows: advice, not a refusal.
`approve` also takes a hostname, labels, a static
address and extra disk roles; see `ichor approve node --help`. A machine
with a weak identity (no UUID, serial or permanent MAC) is refused. After
approval the controller renders and applies the configuration; the node
installs Talos, reboots and joins. With the `single` preset there is nothing
to approve, so skip this step.

If you turned sign-in on, the first of these commands prints this line on
stderr, and so do the commands in [step 4](check-the-cluster.md):

```text
ichor: ichor-ui requires OIDC and there is no usable ichor-ui login; using your own kubeconfig and RBAC in this process instead (to use ichor-ui, run ichor auth login --issuer https://… --audience ichor-ui)
```

It means the CLI could not reach ichor-ui through a login, so it runs the
command itself with your kubeconfig and your Kubernetes permissions. The
command still works, and the later steps run the same way. To go through
ichor-ui and its users instead, sign in once. The `--issuer` is the
`--ui-url` you chose, or `https://localhost:8443` when you gave none (`ichor ui`
must be running while you sign in):

```sh
ichor auth login --issuer https://192.168.1.60 --audience ichor-ui
```

This opens your browser at the login page, so the URL must be reachable from
this machine. If ichor-ui has no certificate of its own, it uses one from the
Ichor serving CA, which your machine does not trust. Export that CA and pass it
to the login:

```sh
kubectl -n ichor-system get configmap ichor-ui-ca -o jsonpath='{.data.ca\.crt}' > ichor-ca.crt
ichor auth login --issuer https://192.168.1.60 --audience ichor-ui --ca-file ichor-ca.crt
```

The warning stops once the login is cached. [Users and login](../users-and-login.md)
explains the CA and `--ca-file`, which the other `ichor auth` commands and the user
commands take too.
