---
sidebar_order: 1
---

# Getting started

From a downloaded `ichor` CLI to a running cluster managed by Ichor.

> [!NOTE]
> For an existing Talos cluster, use [Adopt a running cluster](adopt-a-cluster.md).
> To build the CLI and images yourself, see [Build from source](../development/build-from-source.md).

![Five steps in a row: install the CLI, create the cluster, approve machines, check the cluster, then open the UI and sign in. Step 3 applies to the standard preset only; the single preset skips it.](../images/getting-started-steps.light.drawio.svg)

*The five steps. The single preset skips step 3.*

1. [Install the CLI](getting-started/install-the-cli.md)
2. [Create the cluster](getting-started/create-the-cluster.md): `ichor init --preset single` against the node (default `standard` has three control planes)
3. [Approve machines](getting-started/approve-machines.md)
4. [Check the cluster](getting-started/check-the-cluster.md)
5. [Open the UI and sign in](getting-started/open-the-ui-and-sign-in.md)

## Before you begin

| You need | Why |
| --- | --- |
| A machine booted from a Talos ISO into maintenance mode, reachable on port 50000 (this guide uses `192.168.1.50`), with at least 2 GiB of memory for a control plane | `ichor init` configures it over the Talos API |
| At least 2 GiB of memory on a worker that will run Ichor's controller, which asks for 256 MiB and may use 512 MiB | Talos's own floor for a worker is 1 GiB and it recommends 2 GiB; on less, the controller is starved and restarted during upgrades |
| Network access from the machine to `ghcr.io` (Ichor's images), the Talos installer image and Cilium's images | Air-gapped installs are out of scope, except a self-hosted Image Factory, named by `spec.imageFactoryURL` on the `TalosCluster` (default `https://factory.talos.dev`) |
| A Linux or macOS workstation (amd64 or arm64) reaching port 50000 on the machine and, once up, port 6443 on the endpoint address | init, `kubectl` and the later steps use them |
| [`kubectl`](https://kubernetes.io/docs/tasks/tools/) | Steps 3 and 4; the CLI does not bundle it |

> [!IMPORTANT]
> Boot Talos v1.14.5 or newer: `ichor init` refuses an older node before it
> writes anything. Ichor does not boot machines; use an ISO, PXE or anything else.
