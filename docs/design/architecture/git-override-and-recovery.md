---
sidebar_group: "Trust and recovery"
sidebar_order: 12
---

# Git, override and recovery

A user may keep Ichor's CRs in their own Git and apply them with any tool. A write from the UI or CLI lands in the cluster and the user's tool
decides what to do with the drift. Putting runtime-written state in Git (`TalosOperation`s,
unapproved machines, the labels and annotations Ichor writes on Nodes and on
the storage system's node objects) makes that tool and Ichor fight, so keep
those out. Ichor writes no repository skeleton.

Override is the Kubernetes RBAC verb `override`. It guards the dangerous
operations: removing an Ichor finalizer from a deleting object, resetting a
node, restoring etcd and overriding the last-worker guard. It is checked with a
SubjectAccessReview made as the user.

Upgrades always run through the installer Job. Its CRD check lets only the
retired fields disappear and refuses every other removed field.

The CLI keeps a way out of the corners: `ichor upgrade ichor VERSION --local`
(no ichor-ui) and `ichor restore cluster`.
