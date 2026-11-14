---
sidebar_group: "Configuration"
sidebar_order: 16
---

# Configuration reconcile

1. Load the bundle from the Secret and generate role-specific base configs
   with the pinned version contract.
2. Collect patches by layer and priority and apply them.
3. Validate, hash, and compare with the node's current config via COSI.
4. If different, apply. The mode (try, no-reboot, reboot, staged) is derived
   from the diff, because Talos will not derive it for us — its `AUTO` mode is
   a hardcoded alias for `NO_REBOOT` and never inspects the change.
   A dry run first is still worth it: it is side-effect free and returns
   validation warnings worth surfacing. Reboot-requiring changes take the cluster lock and go one
   node at a time through the operation hook and drain, control planes with
   etcd checks in between. They never start by themselves: the machine reports
   `RebootRequired` with the fields, and a `config` operation, started from
   `ichor rollout config` or Apply in the Overview page's attention list after a plan, rolls them out.
   A third class, `APIServerRestart` (for example `spec.authentication`), needs
   no reboot but restarts the kube-apiserver; the machine controller never
   applies it to a control plane, and the same `config` operation does, one
   control plane at a time with no drain, waiting for that node's `/readyz`
   before the next.
   Talos's `Reboot` does not cordon or drain (unlike its `Upgrade` and `Shutdown`),
   so the rollout does both itself before the reboot and uncordons afterwards.
5. Watch COSI for state and drift. Drift handling is per cluster: warn only
   (default), or auto-correct non-reboot differences. A change in what the
   renderer emits (an Ichor upgrade, `render.Version`) is a desired-state change,
   not drift: an Ichor upgrade may apply configuration to nodes, and
   reboot-requiring changes wait for a config rollout.
