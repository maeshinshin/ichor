# 0175. Every Ichor object lives in the controller's namespace

Status: Accepted · Date: 2026-11-26 · Design section: CLI and UI

Decided: from v0.4.0 a new `TalosCluster`, `TalosMachine`, `TalosConfigPatch`, `TalosOperation`, `TalosEtcdBackup`, `TalosStorage`, `TalosCredentialRequest`, `MachineReclaim` or `IchorUser` is created only in the namespace Ichor's controller runs in, and the validating webhook refuses it anywhere else. An object already elsewhere keeps reconciling and is flagged, never moved or deleted.

## Context

The controller reads the secrets bundle Secret, the registry credentials and the S3 credentials only from its own namespace (ADR 0011, 0019), and its upgrade migrations cover only that namespace. A cluster created elsewhere was therefore never able to come up on its own; `ichor init` already refused another namespace (0019), and `-n` stayed on every other command as a choice with one working answer.

`IchorInstallation` is cluster-scoped and has no namespace; it is not part of this rule.

## Decision

- **New objects.** One validating webhook (`/validate-ichor-maesh-dev-v1alpha1-namespace`, CREATE of the nine namespaced kinds; the chart carries its own copy, and a test derives the kinds from the CRDs and checks both) refuses a create whose namespace is not the controller's, naming that namespace. The controller's own identity and the Kubernetes namespace and garbage collectors are exempt, so Ichor keeps creating the children (machines, operations) of an object that is still outside. Updates and deletes are never checked.
- **Existing objects.** They keep reconciling. Each one outside the namespace gets the condition `OutsideControllerNamespace` (status `True`), written by one shared reconciler registered for every kind that has `status.conditions`. It is informational: `Ready` is untouched and nothing waits on it. `TalosCredentialRequest` has no condition list and is answered once, so it is not flagged.
- **CLI.** `-n/--namespace` is hidden and deprecated on every command that still had it, and accepts only the controller's namespace, the one `--ichor-namespace` names (`ichor-system` unless a Helm install chose another; `ichor ui` and `migrate patches` gain that flag); any other value is refused, before anything is read or written, with one message shared by `init`, `adopt` and the shared flag. `ichor migrate patches` still defaults to every namespace, so a patch left elsewhere is migrated. `installer run` and `installer check` keep a hidden `--namespace`, because the controller's own Job passes the namespace it really runs in, which a Helm install may choose.
- **UI.** `CreatePatch` writes into the controller's namespace instead of the one in the request; the other writes already used it or took it from an existing cluster.

## Consequences

- A user with objects outside the namespace can read, edit and delete them, but cannot start a new `TalosOperation`, backup or patch next to them: those are creates in that namespace. To use Ichor's operations on such a cluster the objects have to be recreated in the controller's namespace; Ichor does not move them, because a move is a delete and losing a user's objects is not allowed.
- A script that passes `-n other` now fails, before anything is written.
- The webhook is `failurePolicy: Fail`, like the others.

## Why not cluster scope

Making the kinds cluster-scoped would remove the question, but a CRD's scope is immutable: it needs a new API group and a conversion of every stored object, and cluster-scoped CRDs need an automatic upgrade migration. That is being studied separately. This ADR is the smaller step that stays valid either way: one namespace is what the controller already assumes.
