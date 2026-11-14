---
sidebar_order: 1
---

# Motivation

Running Talos with `talosctl` works well for one person and a handful of
nodes. It gets awkward when machine configs live in a directory of YAML files,
when several people need to see what's applied where, and when upgrades have
to be sequenced by hand. Putting the configuration into the Kubernetes API
gives you RBAC, audit, webhooks, and a place to hang a UI, without
inventing a new control plane.

## Goals

- Declarative machine configuration as Kubernetes resources, with drift
  detection and a view of the effective config per node.
- Node lifecycle: discover a node in maintenance mode, approve it in a UI,
  install, join, and later remove it safely (drain, etcd membership, reset).
- Rolling upgrades of Talos and Kubernetes, with the cluster lock, etcd quorum
  and storage evacuation taken care of.
- etcd backup to S3-compatible storage, and recovery from nothing but the
  secrets bundle and a snapshot.
- Bootstrap of Cilium for a brand-new cluster, with no Git repository needed.
- CLI and UI parity: anything you can do in one, you can do in the other.

## Non-goals

- Getting machines to maintenance mode. Boot the ISO however you like.
- Storage or monitoring, and any CNI other than Cilium.
- Workloads. Ichor manages the cluster (Talos, Kubernetes, Cilium) and not what
  runs on it, and it does not sync its own resources with a Git repository; GitOps
  of workloads, or of Ichor's resources, is the user's tooling.
- Replacing the storage system's own controller. Volumes, replicas and disk
  state belong to Longhorn (or whatever you run).
- Multi-cluster management. One Ichor manages one cluster, and the UI is built
  for that.
- Air-gapped environments. Ichor builds nothing for an
  installation with no route to public registries; registry mirrors exist for
  pull-through caches and private registries.
- IPv6, PXE provisioning. Later, maybe.
- Know about GitOps tools. Ichor does not detect GitOps tools, does not open
  pull requests and does not refuse a write because a tool tracks the resource.
