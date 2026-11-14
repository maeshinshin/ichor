# 0088. A renderer change is a desired-state change, not drift

Status: Accepted · Date: 2026-10-27 · Design section: Configuration reconcile

Decided: `render.Version` (an integer in `pkg/render/version.go`) is part of every machine's input hash (`desiredInputsHash`), so an Ichor release whose renderer emits different output for the same CRs reads as a desired-state change and is applied like any other, while a node that changed on its own with the same version is still drift; the version is bumped by hand in any change that alters render output, and `TestRenderVersionMatchesOutput` fails when the output of a fixed set of inputs (secrets removed) changes without Version and the pinned fingerprint moving together

## Context

The input hash (`status.observedInputsHash`) covered only the generations of the cluster, machine, applicable patches and storage. After a self-upgrade (ADR 0059) to a release that renders `spec.hostname` into a `HostnameConfig`, every node differed from the render while no generation had moved, so each was reported `NodeDrifted` and, under the default `driftPolicy: warnOnly`, never corrected. Nobody changed the node; Ichor's own desired state changed.

## Decision

- `desiredInputsHash` includes `render=<render.Version>`. A new version makes the hash differ from `status.observedInputsHash`, so the reconcile takes the desired-state path: reboot-free changes are applied with `NO_REBOOT`; reboot-requiring ones set `RebootRequired` and wait for `ichor rollout config` (ADR 0080); install-time-only and unsupported ones are reported as before. The hash is recorded again once the node matches.
- A controller restart on the same release recomputes the same hash, so nothing is re-applied. Only a release with a different `render.Version` applies, once per node. The first release carrying this ADR changes every hash once (the `render=` part is new), which at worst applies the pending render difference, the intended outcome.
- The build version is not used: a rebuild with unchanged rendering must not touch nodes.
- Enforcement is a test, not a review habit. `TestRenderVersionMatchesOutput` renders a control plane and a worker under two different secrets bundles and fingerprints every key path of the output, keeping a value only where both renders agree (so secrets drop out and Ichor's own decisions stay in). The test pins `Version` and that fingerprint; a render change fails it with instructions to bump `Version` and update both pins. It sees only what its inputs exercise, so a change visible only under other inputs (a CNI, a storage layout) must bump `Version` by the author's judgment, and the inputs can be widened when one is missed.
- Docs: an Ichor upgrade may apply configuration to nodes when the renderer changed; changes that need a reboot wait for a config rollout. Nothing about this is in the upgrade plan (the new release's render version is not known to the old controller), so the note lives here and in the design's Configuration reconcile step 5.
