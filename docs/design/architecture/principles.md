---
sidebar_group: "Purpose"
sidebar_order: 5
---

# Principles

1. Machine config is never stored in a CR. CRs hold the ingredients
   (versions, patches, per-node values); the controller renders on every
   reconcile.
2. Secrets are referenced, never embedded.
3. Status holds hashes and state, never rendered config.
4. The Talos version contract is pinned in the CR, so a library bump cannot
   silently change what gets applied.
5. The controller never writes `spec`, so it never fights whatever tool the user
   keeps the CRs in. (Automatic patch upgrades, off by default, keep this
   true: a separate ServiceAccount, `ichor-auto-patch`, which the controller
   impersonates and the webhook limits to a newer patch of the running minor,
   writes the two version fields as a delegated user, and the audit log shows
   it)
6. Disruptive operations (upgrades, reboots, CA rotation, topology changes)
   take a per-cluster Lease. One at a time.
7. Progress lives in status and resumes idempotently. (A live etcd restore
   cannot keep a status, since it rolls the API back; its marker in the backup
   storage is the exception)
8. CLI and UI are equivalent. The only CLI-only operations are the ones that
   happen before a cluster exists.
9. There is always a recovery path that needs no working controller.
10. Dangerous-operation checks live in shared code and are enforced by the UI
    backend and by webhooks, never only in the frontend.
