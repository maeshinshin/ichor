---
sidebar_group: "Configuration"
sidebar_order: 16
---

# Configuration reconcile

![The reconcile loop: base configs from the bundle, patches by layer and priority, validate, hash and compare with the node through COSI, then apply a difference. A difference needing no reboot is applied by the machine controller; a reboot-requiring one waits for a config operation; an API server restart is applied only by the config operation, one control plane at a time. COSI is then watched for state and drift.](../images/configuration-reconcile.light.drawio.svg)

*A difference is applied by the machine controller only when it needs no reboot.*

1. Load the bundle from the Secret and generate role-specific base configs with the pinned version contract.
2. Collect patches by layer and priority and apply them.
3. Validate, hash, and compare with the node's current config via COSI.
4. If different, apply. The mode (try, no-reboot, reboot, staged) is derived from the diff: Talos's `AUTO` is a hardcoded alias for `NO_REBOOT` and never inspects the change. A dry run goes first; it has no side effects and returns validation warnings worth surfacing.
5. Watch COSI for state and drift. Drift handling is per cluster: warn only (default), or auto-correct non-reboot differences. A change in what the renderer emits (an Ichor upgrade, `render.Version`) is a desired-state change, not drift: an Ichor upgrade may apply configuration, and reboot-requiring changes wait for a config rollout.

| Class | Behaviour |
|---|---|
| Reboot-requiring | Never starts by itself: the machine reports `RebootRequired` with the fields. A `config` operation (`ichor rollout config`, or Apply in the Overview attention list after a plan) takes the cluster lock and goes one node at a time through the operation hook and drain, control planes with etcd checks in between. Talos's `Reboot` neither cordons nor drains (unlike `Upgrade` and `Shutdown`), so the rollout does both before the reboot and uncordons after. |
| `APIServerRestart` (for example `spec.authentication`) | No reboot, but kube-apiserver restarts. The machine controller never applies it to a control plane; the same `config` operation does, one control plane at a time with no drain, waiting for that node's `/readyz` before the next. |
