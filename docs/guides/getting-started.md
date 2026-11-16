---
sidebar_order: 1
---

# Getting started

This guide takes you from a downloaded `ichor` CLI to a running one-node
cluster managed by Ichor. For an existing Talos cluster see
[Adopt a running cluster](adopt-a-cluster.md) instead. To build the CLI and the
images yourself, see [Build from source](../development/build-from-source.md).

## Before you begin

- One machine (virtual or bare metal) booted from a Talos ISO into maintenance
  mode, with an address your workstation can reach on port 50000. This guide
  uses `192.168.1.50`. Talos needs at least 2 GiB of memory on a control plane.
  Ichor does not boot machines. The machine must run Talos v1.14.5 or newer, the
  release this Ichor installs: `ichor init` reads the node's version and
  refuses an older one before it writes anything, so boot an ISO of that
  release.
- Network access from the machine to `ghcr.io` (Ichor's images), and for the
  Talos installer image and Cilium's images. Air-gapped installs are out of
  scope.
- A Linux or macOS workstation (amd64 or arm64) that can reach the machine on
  port 50000 and, once the node is up, the cluster's endpoint address on port
  6443: `ichor init` connects to the Kubernetes API through that address to
  install Ichor, and so do `kubectl` and the later steps.
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/) on the workstation. The
  CLI does not bundle it, and [step 3](getting-started/approve-machines.md) and
  [step 4](getting-started/check-the-cluster.md) use it.

## The steps

1. [Install the CLI](getting-started/install-the-cli.md): download, check and unpack a release.
2. [Create the cluster](getting-started/create-the-cluster.md): run `ichor init --preset single` against the node. Without `--preset` the cluster has three control planes.
3. [Approve machines](getting-started/approve-machines.md): add and approve further nodes.
4. [Check the cluster](getting-started/check-the-cluster.md): read its state with the CLI.
5. [Open the UI and sign in](getting-started/open-the-ui-and-sign-in.md): reach ichor-ui.
