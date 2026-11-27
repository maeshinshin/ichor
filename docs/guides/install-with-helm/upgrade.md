---
sidebar_order: 9
---

# Upgrade and uninstall

![ichor upgrade ichor is refused on a Helm install and prints the Helm command; helm upgrade upgrades it. helm uninstall keeps the CRDs and every cluster object.](../../images/ih-upgrade.light.drawio.svg)

*Helm owns upgrade and uninstall.*

- Upgrade with `helm upgrade`; the CRDs are in the chart's templates and upgrade with it.
- An upgrade from v0.2.x moves each patch's `spec.documents` to `spec.config` by itself: a post-upgrade hook Job runs the controller image's `migrate-patches` under a ServiceAccount that exists only while it runs. A patch it cannot move is left as stored with `Migrated=False`, and the upgrade still succeeds; `ichor migrate patches` does it again by hand. The controller applies a change that needs no reboot by itself. `ichor rollout config` rolls out only what waits for you: nodes at `RebootRequired`, and control planes at `APIServerRestartRequired` (the restart that sign-in needs). A migration alone changes nothing on a node, because a setting that only moved renders the same.
- `ichor` commands that would run the installer Job refuse and print the Helm command. The UI's Helm detection is limited, so some of its upgrade prompts may still describe the installer path.

> [!IMPORTANT]
> `helm uninstall` keeps the CRDs by default (`crd.keep: true`), and so every cluster object. Cilium is a separate release and stays.

> [!WARNING]
> These steps apply to a manifest install too. The namespace Ichor runs in carries the label `ichor.maesh.dev/protected` (the controller sets it at start-up) and cannot be deleted until you say so. Deleting it takes the controller down while the clusters it manages are still there, and the namespace then stays `Terminating` for good. To remove Ichor on purpose:
>
> 1. Export each bundle: `ichor export bundle NAME --bundle-passphrase-file PATH` (the file holds the passphrase; the bundle is encrypted, which is recommended). With no terminal, a new bundle needs that flag or `--bundle-plaintext`, which stores it unencrypted.
> 2. Remove the clusters: set the annotation `ichor.maesh.dev/allow-delete=true` on each TalosCluster and delete it, then set the annotation on each of its TalosMachines and delete them. The nodes keep running and are no longer managed; a TalosMachine whose TalosCluster still exists is held to the control-plane quorum guard, so the order matters.
> 3. Remove every other Ichor object in the namespace (the IchorUser `admin` that `ichor init` and `ichor adopt` create, among them) while the webhook still runs: `kinds=$(kubectl api-resources --api-group=ichor.maesh.dev -o name --namespaced | paste -sd,)`, then `kubectl -n ichor-system annotate "$kinds" --all ichor.maesh.dev/allow-delete=true` and `kubectl -n ichor-system delete "$kinds" --all`. The webhook is gone before the namespace controller reaches them, and then no delete can pass.
> 4. Annotate the Secrets labelled `ichor.maesh.dev/protected` (one secrets bundle per cluster, and the Cilium prior release if adopt took one over): `kubectl -n ichor-system annotate secret -l ichor.maesh.dev/protected ichor.maesh.dev/allow-delete=true`.
> 5. Delete the cluster-scoped IchorInstallation while the webhook still runs: `kubectl annotate ichorinstallation ichor ichor.maesh.dev/allow-delete=true`, then `kubectl delete ichorinstallation ichor`. Deleting it makes the controller do nothing.
> 6. Annotate the namespace `ichor.maesh.dev/allow-delete=true`, then `kubectl delete namespace ichor-system`.
> 7. A manifest install leaves its cluster-scoped objects: the validating and mutating webhook configurations, the admission policies and their bindings, and the ClusterRoles and ClusterRoleBindings. A webhook configuration that points at the deleted Service refuses every later write to an Ichor kind. Download the `install.yaml` of the release you installed, `curl -fsSLO https://github.com/maeshinshin/ichor/releases/download/vX.Y.Z/install.yaml` (a release asset from v0.3.6 on; for v0.3.0 to v0.3.5 take the v0.3.6 one, which names the same objects and four more that an older install lacks, `--ignore-not-found` skips them; `ichor_X.Y.Z_checksums.txt` lists its sha256 and is signed), then delete with it: `kubectl delete -f install.yaml --ignore-not-found`. That file also holds the CRDs, so this deletes the CRDs and every Ichor object still left. Keeping the CRDs is the default and deleting them is optional.
>
> A Helm install is simpler: `helm uninstall` removes the webhook configurations, admission policies, ClusterRoles and ClusterRoleBindings, and keeps only the CRDs (and the Secrets and namespace you handle in the steps above). Steps 1 to 6 still apply; step 7 does not, unless you also want the CRDs gone.
>
> If a namespace is already stuck, see [Recover a namespace stuck in Terminating](../troubleshooting.md#recover-a-namespace-stuck-in-terminating).

For an Ichor the installer made, see [Upgrade Ichor with the installer](../upgrade-ichor.md).
