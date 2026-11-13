---
sidebar_order: 4
---

# 4. Check the cluster

The commands below use [`kubectl`](https://kubernetes.io/docs/tasks/tools/) once,
to list the nodes.

```sh
export KUBECONFIG=~/.ichor/clusters/demo/kubeconfig
ichor get cluster demo
ichor describe cluster demo
ichor get nodes --cluster demo
ichor version
kubectl get nodes
```

```console
$ ichor get cluster demo
demo · ichor-system
  Talos        v1.14.5
  Kubernetes   v1.36.6
  Cilium       1.20.4
  Endpoint     192.168.1.50 (node)
  Health       ✓ ready

$ ichor get nodes --cluster demo
NAME      ROLE           APPROVAL  READY  ENDPOINT            KUBERNETES NODE  AGE
fa70962a  control-plane  approved  ✓      192.168.1.50:50000  demo-cp          5m

$ ichor version
ichor        v{{version}}
installed    v{{version}}
new installs Talos v1.14.5 · Kubernetes v1.36.6 · Cilium 1.20.4 (contract v1.14)
```

`ichor get cluster NAME` prints versions, endpoint and health in a few lines;
`ichor describe cluster NAME` adds the network, CNI, control plane, certificate expiry and
every condition (with ✓, `!`, ✗ or … in front). Every machine should be Ready
(✓). `ichor version` shows the CLI and the installed Ichor version; they should
both say the release you installed.
With sign-in on, these commands also print the sign-in line from [step 3](approve-machines.md) on
stderr. The output below leaves it out.

The CLI prints for people. In a script, add `-o yaml` or `-o json` to any
command for the full response. A table cell is never cut off, but a long
machine name is shown in short form (see [step 3](approve-machines.md)); `-o yaml` prints it whole.
Colour appears only on a terminal (set `NO_COLOR` to turn it off).

The same goes for the operations. `ichor get operations` shows each one with a
glyph for its phase and what it is doing; `ichor describe operation NAME` adds the
state of every machine. `--follow` (on `upgrade`, `rollout config` and the node
actions) and `ichor describe operation NAME --watch` keep one live line with the phase and
message, and end it with the time the operation took. In a pipe or CI log they
print a line per change instead:

```console
$ ichor get operations
NAME                 KIND    PHASE         TARGET  MESSAGE                       AGE
upgrade-talos-x7k2p  talos   … InProgress  demo    upgrading talos-cp2 (2 of 3)  12m
config-r2d9m         config  ✓ Completed   demo    -                             3d
$ ichor upgrade talos --follow --yes
Upgrade Talos v1.14.3 → v1.14.5
operation upgrade-talos-x7k2p · cluster demo
… talos — InProgress · 0/3 nodes · upgrading talos-cp1
… talos — InProgress · 1/3 nodes · upgrading talos-cp2
✓ talos — Completed (11m52s)
```

A failed step ends the line with ✗ Failed, prints the reason and exits with an
error that names `ichor resume operation NAME`. `ichor upgrade talos --list`
(and `kubernetes`) prints the versions on offer as a table; `-o yaml`
and `-o json` of these commands print what they always printed.
