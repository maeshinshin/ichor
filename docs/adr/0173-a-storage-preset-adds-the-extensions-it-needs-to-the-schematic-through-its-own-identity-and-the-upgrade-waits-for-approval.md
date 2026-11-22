# 0173. A storage preset adds the extensions it needs to `spec.schematic` through an identity of its own, and the upgrade that applies them waits for a person

Status: Accepted, amended · Date: 2026-11-22 · Design section: Storage · Touches: 0023, 0116, 0167

Decided: when the nodes a `TalosStorage` governs lack the Talos system extensions its preset needs, the controller registers a schematic with those extensions at Image Factory, writes its ID to `TalosCluster.spec.schematic.id` through a ServiceAccount used only for that write, and creates a `kind: talos` `TalosOperation` with `spec.paused: true`. Nothing reboots until a person approves that operation (`ichor approve operation`). Until the nodes report the extensions, `TalosStorage` is `Ready=False` with reason `ExtensionsMissing`, naming the extensions and the operation.

## Context

`longhorn-v1` needs `siderolabs/iscsi-tools` and `siderolabs/util-linux-tools` (`longhornV1.RequiredExtensions`). `machine.install.extensions` adds nothing: the Talos v1.14 system-extensions page says "the way to install system extensions in the `.machine.install` section of the machine configuration is now deprecated" (https://docs.siderolabs.com/talos/v1.14/build-and-extend-talos/custom-images-and-development/system-extensions), and the CHANGELOG records no effect from Talos 1.10 (ADR 0023, amendment 2026-11-22). Extensions come only from an Image Factory schematic and an upgrade to its installer image: "generate new schematic with the new set of system extensions, and upgrade the machine to the new schematic ID" (https://docs.siderolabs.com/talos/v1.14/platform-specific-installations/boot-assets). A schematic is registered with `POST https://factory.talos.dev/schematics`, and its ID is a function of its content, so the same content returns the same ID. The upgrade reboots every node; the schematic is cluster-wide (`spec.schematic`), so there is no per-node form.

Today `TalosStorage` reports `Ready=True` with `ExtensionsPresent=False` (`MissingExtensions`) and says to edit `spec.schematic` and run `ichor upgrade talos`. A GitOps health check reads `Ready`, so a preset that cannot work looks healthy. Observed on 2026-11-21 on a KubeVirt lab.

The controller never writes a `TalosCluster` spec (principle 5). ADR 0116 already records one exception, the ServiceAccount `ichor-auto-patch`, which the controller impersonates.

## Decision

1. **Trigger.** On reconcile of a `TalosStorage` whose preset names required extensions, when at least one governed node's Kubernetes Node lacks an `extensions.talos.dev/<name>` label for one of them (the check `ExtensionsPresent` already makes), and the controller has the identity below configured.
2. **The schematic.** Ichor registers `customization.systemExtensions.officialExtensions: <required names, sorted>` at Image Factory and takes the returned ID. A schematic ID names no content and Image Factory is not asked for the content behind one, so the merge is exact only when the current schematic is known. It is known in two cases: `spec.schematic` unset or the stock ID (`upgrade.DefaultSchematic`), and an ID already equal to the one Ichor would write. Any other ID is a schematic Ichor cannot read, so Ichor writes nothing: `Ready=False`, reason `SchematicNotMergeable`, message naming the extensions and the person's one step (build a schematic that includes them and set `spec.schematic.id`). `shortcut:` the additive merge is over the stock schematic only; it widens when Ichor stores the content of schematics it registers.
3. **The write, and its limits.** One field, `spec.schematic.id`, written by a JSON merge patch from the ServiceAccount `ichor-storage-extensions`, which the controller impersonates through a namespaced Role naming that one account. The account holds `patch` on `talosclusters` and nothing else. The admission webhook refuses any write by that username that changes anything but `spec.schematic.id`, or that changes it from a value other than unset or the stock ID, or to an empty value. The write is idempotent: an equal ID is not written again. The controller's own username still cannot write a spec. The audit log shows the write as `system:serviceaccount:<namespace>:ichor-storage-extensions`.
4. **The upgrade waits for approval, with the mechanism that exists.** The controller creates, with its own identity (it already creates operations), a `TalosOperation` of kind `talos`, `spec.paused: true`, annotated `ichor.maesh.dev/storage-extensions: <schematic ID>` and `ichor.maesh.dev/started-by: ichor-storage`. A paused operation holds no cluster lock and advances no machine (`talosoperation_controller.go`). Approving is `ichor approve operation <name>` or Approve in the UI; no CRD field is added. An operation that is paused and has run no step reports "waiting for approval" instead of "spec.paused is true". At most one operation is created per schematic ID: the annotation is the record, so a pass that finds it creates nothing, and a deleted operation is created again on the next pass while the extensions are missing.
5. **Ready.** While a governed node lacks a required extension, `Ready` is False with reason `ExtensionsMissing`; the message names each node and its missing extensions, and the operation and its state (waiting for approval, running, failed). `ExtensionsPresent` stays as the detail condition. A node with no Kubernetes Node yet leaves `ExtensionsPresent` Unknown and does not hold `Ready`, as before.
6. **Where it calls out.** The controller POSTs to `factory.talos.dev` (the host `pkg/secureboot` already names), from the controller pod, on 443. The chart's Cilium policy already allows the controller `world` on the external ports. When Image Factory cannot be reached, `Ready=False`, reason `SchematicUnavailable`, and the next pass retries.

## Consequences

- This is a second recorded exception to "the controller never mutates `spec`", with the same shape as the first. `CLAUDE.md`, `docs/design/architecture/principles.md` and the architecture page name it. It is narrower than the first: one field, one value family (an ID Image Factory returned for the preset's extensions), over the stock schematic only.
- A person still decides when the whole cluster reboots. Between the write and the approval, `spec.schematic` and the nodes differ; the plan the CLI and UI show for an upgrade already names a schematic-only change (ADR 0023, amendment 2026-10-27).
- Creating a `TalosStorage` with `longhorn-v1` on a cluster with custom schematic needs one manual step, and says so.
- The extra identity adds a ServiceAccount, a ClusterRole, a binding, an impersonation Role and Binding, and a webhook branch; no CRD field.

## Alternatives considered

- Widen `ichor-auto-patch`. Rejected: that account may write only version fields, and a second purpose widens a delegated identity whose limits the webhook states in one sentence.
- Run the upgrade automatically after the write. Rejected by the maintainer: it reboots every node, including nodes governed by another preset.
- Keep `Ready=True` and only document it. Rejected: the health signal is the one thing a GitOps consumer reads.
- Merge into a custom schematic. Not possible without its content; see Decision 2.

## Amendment, 2026-11-22: the Image Factory URL is a setting of the cluster

Decision 6 first fixed the host as `factory.talos.dev`. The maintainer decided that v0.3.0 has a setting for a self-hosted or air-gapped Image Factory. It is `TalosCluster.spec.imageFactoryURL`, an optional string, unset meaning `https://factory.talos.dev` (a new field, so append-only). The webhook refuses a value that is not an https URL with a host and an optional port and nothing else (no path, query or credentials), on create and update.

**Why the cluster.** The schematic (`spec.schematic`) and the installer image of every node are the cluster's, and one cluster has one Image Factory. A `TalosStorage` preset scope would let two presets of one cluster name two factories, and a schematic registered at one could not be pulled from the other. `IchorInstallation` is the install of Ichor, not of a Talos cluster.

**What reads it.** Two places build a URL from the host, and both read the field.
- The schematic registration (`ImageFactoryClient.Register`) posts to `<spec.imageFactoryURL>/schematics`, and what it remembers is keyed by factory as well as by extensions.
- The installer reference Ichor renders (`secureboot.ClusterInstallerImage`: the Talos upgrade, `ichor init`, and the install default of an approval) is `<host[:port]>/metal-installer/<schematic>:<version>`, with the host taken from the field. `secureboot.InstallerImage`, which takes no cluster, stays the stock host and has callers only in tests. The remaining strings that name `factory.talos.dev` are comments and the text of a webhook warning about SecureBoot, not URLs Ichor builds.

What a self-hosted factory must serve is what the stock one does: `POST /schematics` answering `{"id": ...}` (Talos v1.14, "Boot Assets"), and the installer repositories `metal-installer` and `metal-installer-secureboot` at the host. The controller reaches the factory over https from its pod; the chart's Cilium policy allows `world` on the external ports, and `networkPolicy.cilium.externalCIDRs` narrows it. A node pulls the installer from the same host, so its registry mirror configuration (`ichor init --registry-mirror`) is the user's to set for that host.

## Amendment, 2026-11-22: an operation that never ran is approved, not resumed

"Resume" is wrong for an operation that has not started. Such an operation is **approved**, as a pending machine is (`ichor approve node`).

- The creator marks an approval-gated operation with the annotation `ichor.maesh.dev/awaits-approval: "true"` (the storage preset sets it). The controller sets the Paused condition's reason to `AwaitingApproval` when `spec.paused` is true, the mark is present and the operation has no machine, node, stage or failure in its status. A pause without the mark is a person's: `OperatorPaused`. The condition is what the CLI and the UI read, so any approval-gated operation can use it. The message reads "waiting for approval … ichor approve operation NAME".
- `ichor approve operation NAME` clears `spec.paused`, the same write as resume, through the same RPC and the same permission. It refuses any other operation and names `ichor resume operation` for a failed one or one a person paused. `ichor resume operation` still works on an operation waiting for approval.
- The UI shows an Approve button with a line that says it starts an upgrade that reboots each node, instead of Resume. The audit record of the write has the action `approve`.
- `AwaitingApproval` is not a failure: it sets no `status.failureReason`, and the Paused condition is a plain `True` one.
