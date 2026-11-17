---
sidebar_group: "Purpose"
sidebar_order: 23
---

# Alternatives considered

Raw YAML patches. Easier to start with, impossible to validate, preview or
explain in a UI, and they tie user files to a Talos version. Rejected in
favour of typed documents.

Ichor installing storage and monitoring itself. It would double the surface
area, duplicate what the storage controllers already do, and keep
us on the upgrade treadmill of every project we wrap. Ichor installs Cilium only.

Fewer CRDs. `TalosEtcdBackup` and `TalosStorage` could fold into
`TalosCluster`. They are kept separate so their RBAC and Git diffs stay
focused; merging later would need a migration, splitting later wouldn't.

Multiple repositories. The three deliverables share the CRD types and the
Connect definitions; separate repositories would mean a fourth one for those
and version skew to manage. One repository, three release artifacts.
