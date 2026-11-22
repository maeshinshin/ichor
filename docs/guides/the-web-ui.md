# The web UI

`ichor-ui` serves one web app. It shows the one cluster Ichor manages and writes
only Custom Resources: every change is a resource the controller then acts on.
For how to reach it and sign in, see
[Open the UI and sign in](getting-started/open-the-ui-and-sign-in.md).

![The screens of the web UI: Sign in leads to the sidebar, which has Overview, Nodes, Configuration, Cilium and Storage under Cluster, Operations and Backups under Activity, and Users and Settings under Admin. Nodes leads to the node page and Add node, Configuration to the patch editor, Operations to one operation.](../images/web-ui-screens.light.drawio.svg)

*Thirteen screens: nine in the sidebar and four reached from a list.*

## The shell

| Part | What it does |
|---|---|
| Sidebar | Three groups: Cluster, Activity, Admin. It collapses to icons, and on a phone it opens as a sheet. |
| Top bar | A breadcrumb, the cluster name, its Talos and Kubernetes versions and a health badge (ready, degraded or upgrading). |
| Light/dark switch | At the right end of the top bar. The saved choice wins, otherwise the system setting. |
| Account menu | Who you are, and sign-out. |

- **One cluster.** The UI lists the clusters and shows the first by name. With
  none it says so, with more than one it adds a warning above the page. Users and
  Settings need no cluster. An old multi-cluster URL (`/clusters/...`,
  `/machines`, `/patches`) redirects to its new place.
- **Phone width.** The sidebar becomes a sheet below 768 px and a wide table
  scrolls inside itself, so the page never scrolls sideways.
- **English only.** The words of the UI are fixed; resource names, Talos and
  Kubernetes field names and error bodies appear as received.
- **Permissions.** Kubernetes RBAC decides. The UI asks what you may do and hides
  a button you cannot use, and the server checks again. See
  [Security](../concepts/security.md#roles) for the roles.

## The screens

| Screen | Path | Shows | You can | Gated by |
|---|---|---|---|---|
| Overview | `/` | Endpoint, nodes ready, the running operation, backups, a Versions table (Talos, Kubernetes, Cilium, Ichor), recent operations, rollout and hand-over, notices (pending nodes, patch updates, a newer Ichor release, halted automatic patching) | Start an upgrade from a Versions row; open the "more" menu: rotate CAs, rotate keys, download a read-only talosconfig, shut down the cluster | Each menu item has its own verb: the start verb of its operation, or `download` on `talosclusters/talosconfig-reader` |
| Nodes | `/nodes` | Approved, pending and rejected nodes: identity, role, endpoint, ready | Approve or reject a pending node; open Add node; "Reset and add" to reclaim a machine | `approve` on `talosmachines/approval` and write on the node; `reclaim` on `machinereclaims`; cluster update for Add node |
| Node | `/nodes/NAME` | Six tabs: Summary, Storage & network, Talos, Kubernetes & etcd, Configuration, Logs | The Actions menu (see below) | Per action |
| Add node | `/nodes/add` | Whether discovery is on, the addresses and CIDR ranges scanned (at most 256 per range), nodes waiting for approval | Add or remove an address or range; approve a waiting node | Cluster update; `approve` |
| Configuration | `/config` | The `TalosConfigPatch` list: target, layer, priority, what it applies to | Create, edit, delete a patch | Create, update and delete on `talosconfigpatches` |
| Patch | `/config/new`, `/config/NAME` | Target, layer, priority, a searchable settings form and its `spec.config` as JSON | Preview the impact (does it need a reboot), save | Create or update on `talosconfigpatches` |
| Cilium | `/cilium` | The effective Helm values, the keys Ichor owns, your YAML values, a diff, a newer chart version | Validate, preview and apply your values; start a Cilium upgrade | `update` on `talosclusters/cilium-values` (admin) |
| Storage | `/storage` | Each `TalosStorage`: node selector, disk preset, required extensions, evacuation timeout, nodes it governs | Create one | Kubernetes RBAC, checked by the server |
| Operations | `/operations` | Every operation: kind, state, progress | Search; open one | Read |
| Operation | `/operations/NAME` | What it does, the steps, per-node progress, failure reason, events, who started it | Pause, resume (a failed step runs again), approve one that waits for approval, delete | Kubernetes RBAC, checked by the server |
| Backups | `/backups` | Each `TalosEtcdBackup`: schedule (UTC), destination, retention, last success, snapshots stored | Back up now; restore the cluster to a snapshot | `updateBackups`; `start` on `talosoperations/etcd-restore` |
| Users | `/users` | Ichor users: roles, disabled, ready | Create, edit, set a password, revoke sessions, delete | Listed only if you may list users, and only when Ichor has its own users |
| Settings | `/settings` | Installed and target version, defaults for new clusters, update check, automatic patching, token lifetimes | Save the settings; clear an automatic-patching halt | Admin; Helm installs show a notice |

The sign-in screen is outside the gate. It offers the Ichor password form, an
identity-provider button, or both
([Sign in](users-and-login/sign-in.md)).

## The node page

The Actions menu has up to three groups; it is absent when none apply.

| Group | Entries | Needs |
|---|---|---|
| Downloads | Download support bundle, etcd snapshot (control planes), packet capture | The `download` verb on the matching subresource. See [Investigate a node](troubleshooting.md). |
| Node | Restart service, defragment etcd and forfeit leadership (control planes only) | Create on `talosoperations`. See [Node actions](node-actions.md). |
| Danger zone | Reboot, shut down, remove | Reboot: create on `talosoperations`. Shut down: also the start verb `node-shutdown`. Remove: the start verb `node-reset`, write and delete on the node, and `override` |

A node that is not approved, or is being removed, offers no action.
