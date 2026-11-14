# 0091. A CA rotation waits for the operator's new credentials before anything they hold stops working

Status: Accepted · Date: 2026-10-27 · Design section: Backup, recovery, certificates; Trust · Supersedes in part: 0063 (stage sequence, "How Ichor stays connected" covers Ichor only), 0068 Decision 7 (the CLI start) · Amended by: 0093 (the UI download)

Decided: the `ca-rotation` stages are `Accepting`, `AwaitingCredentials`, `Issuing`, `Dropping`, `Finalizing`, `Done`; `AwaitingCredentials` changes nothing on any node and the controller leaves it only when the operation carries the annotation `ichor.maesh.dev/credentials-handoff` with the value `talos=<new talos CA fingerprint>,kubernetes=<new kubernetes CA fingerprint>` (`ichorv1alpha1.CARotationHandoffToken`); the CLI sets it, the controller only reads it and never writes `spec`; `ichor rotate ca` waits for the gate through the Kubernetes API, then writes a kubeconfig and a talosconfig that trust both the old and the new CAs and are signed by the new ones, checks that the new kubeconfig authenticates to the cluster, and only then sets the annotation; `ichor create credentials NAME` writes the same files from the cluster (`--acknowledge` also sets the annotation) or, with `--bundle FILE`, offline from a `bundle-export` file; `rotate-ca --follow` follows through the Kubernetes API with the new kubeconfig, not through the ichor-ui tunnel; the UI offers no download here, which ADR 0093 reverses: it adds an admin-gated download and a continue button

## Context

A rotation locks the operator out. Within seconds of `Issuing` the kube-apiserver
serves the new CA and the operator's kubeconfig, which trusts the old one only, fails with `x509: certificate signed by
unknown authority`. The nodes serve certificates from the new Talos API CA from `Issuing` on, so the operator's talosconfig,
which trusts the old Talos API CA only, is refused on every node from that pass on, not only after the old CA was dropped
(about two and a half minutes later, at `Dropping`). Both files therefore stop working from the second pass (`Issuing`). Nothing the operator holds reaches the cluster afterwards, `bundle-export` needs a working kubeconfig,
and nothing in Ichor hands the operator new credentials. `talosctl rotate-ca` does: it writes a new talosconfig and builds
a new kubeconfig from the nodes during the rotation (Talos v1.14.3, `pkg/rotate/pki/kubernetes` fetches a client after the
swap, `cmd/talosctl/cmd/talos/rotate-ca.go` saves the new talosconfig). ADR 0063 ran the rotation in the controller and
left that part out. Its `--follow` also prints `API unreachable, retrying (dial tcp [::1]:<port>: connection refused)`
every 15 s: the port-forward to ichor-ui goes through the kube-apiserver, which restarts when it is given the
new CA list, the tunnel dies, and `Reconnect` (`follow_retry.go`) fails every time without saying so
(the old kubeconfig is refused by the same API server) so the failure looks like a hang.

## Decision 1: the gate

After `Accepting` is verified on every machine, every node accepts both CAs for the Talos API and the Kubernetes API and the
old CAs still issue, so every credential the operator holds still works and a credential signed by the new CAs works too.
That is the only moment in the rotation where both are true, so it is where the operator gets the new ones. The stage
`AwaitingCredentials` follows `Accepting`; the operation is `Paused` with the `CARotation` condition `False`/`AwaitingCredentials`
(its message names `ichor create credentials NAME --acknowledge`), requeued every `operationRequeueInterval` like every other
unverifiable step, and it applies nothing. There is no way to skip it: no flag, no annotation value that matches by accident
(the token is the new CAs' fingerprints, so an acknowledgement left from another rotation does not release this one). A
`--skip-credential-handoff` was considered and not built: an operator who does not want credentials still has to say so by running
the acknowledge command, and nothing in the product needs the unattended path (an automated harness can set the
annotation itself). If one is wanted later it must be an explicit field on the operation that the plan states; it must not be a default.

The wait is not a timeout and not a failure: it holds the cluster lock and the finalizer like any stage, the machine controller
stands aside, and nothing is half-done, because a cluster in `AwaitingCredentials` is in the state `Accepting` left it.

## Decision 2: the dual-CA files

`ichor create credentials` and `rotate-ca` write two files, mode 0600 (directory 0700 if created), through the atomic writer of
`bundlefile.go`, never printing their contents:

- **kubeconfig.** The server, cluster, context, user and namespace names are copied from the current kubeconfig (so
  `--kubeconfig`, `$KUBECONFIG` and the default rules behave as for every other command); `certificate-authority-data` holds
  the new Kubernetes CA followed by the old one; the user is a client certificate with CN `admin`, O `system:masters`, signed by
  the new Kubernetes CA (key type follows the CA, one year). Only the current context is kept.
- **talosconfig.** One context named after the cluster; `ca` is the base64 PEM bundle of the new and the old Talos API CA;
  `crt`/`key` an `os:admin` certificate generated with `Bundle.GenerateTalosAPIClientCertificate` from a copy of the bundle whose
  OS CA is the new one. Endpoints are `--talos-endpoints`, else the control-plane machines' `DialEndpoint()`, else the current
  talosconfig's.

Default paths: the current kubeconfig's and talosconfig's path plus `.ichor-new`; `--kubeconfig-out` and `--talosconfig-out`
choose others. The command refuses an output that is the current file, so a failure never costs the operator the credentials that
work. Outside a rotation (no `rotation.yaml`) the same command writes files that trust the one current CA, which is what to run
after the rotation completes.

**Why this works with the Talos trust model.** A kubeconfig may carry several CAs in `certificate-authority-data`; the client
accepts a server certificate chaining to any of them, so it works against the old serving CA (before `Issuing`) and the new one
(after). The Kubernetes API accepts client certificates signed by the CAs in `cluster.acceptedCAs`, which `Accepting` added; the Talos API
does the same for `machine.acceptedCAs`. That is the claim the gate rests on, and Ichor does not take it on trust: before
acknowledging, the CLI asks the API server who the new kubeconfig is (a `SelfSubjectReview` with the new files), and does not set the
annotation if that fails, so a Talos build where an accepted CA does not authenticate clients leaves the rotation waiting, with the
old credentials intact, rather than stranding the operator. The same check against a node's Talos API with the new talosconfig is
made too, but only warns when it fails: the operator's machine often cannot reach the nodes except through the Kubernetes API, and
the Kubernetes check is the one that guards the file the rotation cannot continue without.

## Decision 3: what sets the acknowledgement, and where the CLI reads the CAs

The CAs' keys are in the bundle Secret (`rotation.yaml`, written before `Accepting`, ADR 0063). Making credentials needs those keys,
so the command reads the Secret with the operator's own kubeconfig, as `bundle-export` does, and the keys never travel through the
ichor-ui API or the browser. (ADR 0093 reverses this for the UI: an admin-gated download and a continue button.) For the same reason the UI had **no download** (ADR 0084's downloads are diagnostics; these are
cluster-admin private keys): the start dialog and the operation page say that the credentials stop working, that the operation waits,
and which command to run. Choosing the CLI over a gated download is the simpler choice and the safer one: nothing new crosses the
network, the admin gate and audit of ADR 0084 are not extended to key material, and the only party that can release the gate is one
who holds a working kubeconfig at that moment. The acknowledgement is an annotation on the `TalosOperation` (metadata, not `spec`),
which needs `patch` on `talosoperations` that the operator already has to start the rotation.

`rotate-ca` always performs the hand-off, with or without `--follow`: a rotation started without it would otherwise wait forever.
It waits for the gate by reading the operation through the Kubernetes API with the old kubeconfig (retrying the transient errors of an
API server restarting for the accepted-CA change), writes and checks the files, acknowledges, prints where the files are and how to
use them (`KUBECONFIG=… TALOSCONFIG=…`), and returns; with `--follow` it then watches the operation to its end with a client built from the
**new** kubeconfig. If it is interrupted while the operation waits, `ichor create credentials NAME --acknowledge` does the rest.

## Decision 4: `--follow` and the tunnel

The tunnel to ichor-ui is a port-forward through the kube-apiserver. It dies when the apiserver restarts, and it cannot be rebuilt
with a kubeconfig the apiserver no longer accepts, which after `Issuing` is the operator's old one. `retryTransient` called `Reconnect`
and dropped its error, so the failure repeated silently. Two changes: the error is now printed
(`ichor: could not re-open the tunnel to ichor-ui (...)`) for every `--follow`/`--watch`, and `rotate-ca --follow` does not use the
tunnel after the hand-off: it polls the `TalosOperation` through the Kubernetes API with the new kubeconfig (`watchCARotation`),
which is independent of ichor-ui's pod and of its endpoint, and retries transient errors with the shared helper. The plain
`describe operation --watch` keeps the tunnel and the generic reconnect, now with its reason shown.

## Decision 5: offline credentials

`ichor create credentials NAME --bundle FILE` reads a `bundle-export` file and needs no cluster: it writes the files for the single CA in
the file. It exists for the operator who holds a current bundle (exported after the last rotation) and has lost every kubeconfig and
talosconfig; the Kubernetes server comes from `--kubeconfig` (any kubeconfig naming the server) and the Talos endpoints from
`--talos-endpoints`. A bundle saved before a rotation names CAs no node accepts any more: that is the reason `BundleBackupStale`
exists, and the command does not pretend otherwise.

## Consequences

- The CRD's `status.caRotation.stage` enum gains `AwaitingCredentials`; the chart copy is regenerated. `OperationService` and the proto are
  unchanged apart from the comment on `ca_rotation_stage`; the UI's `caStageProgress` counts six stages.
- The plan text (CLI and UI) states that the operator's kubeconfig and talosconfig stop working, that the operation waits for the
  hand-off, and how it is done. The destructive alert in the dialog is shown for every `ca-rotation` plan.
- A rotation started from the UI waits at the gate until someone runs the CLI. It cannot stall the cluster: the lock is held, the
  machine controller stands aside, as in any stage; `Paused/AwaitingCredentials` is the visible state.
- `rotate-ca` now needs the operator's kubeconfig to read the Secret and the operation directly; `bundle-export` already did.
  Where the operator has only ichor-ui access, the rotation waits.
- Not built (the UI download was added by ADR 0093): `--skip-credential-handoff`, writing the new files over the current ones, refreshing a kubeconfig that names several
  clusters (only the current context is written), and credentials for the etcd and aggregator CAs (not rotated, ADR 0063).
- ADR 0063's statement that "Ichor's dial" is the only client it keeps connected stands: the controller's bundle-based dial is
  unaffected; this ADR is about every other client the operator owns.

Amendment 2026-11-06: `ichor rotate ca` without `--follow` still stays until the hand-off is done (Decision 3 stands: the files and the acknowledgement need the operator's own kubeconfig, and the CLI is the one party that has it); it returns once the operation has gone on, in seconds when the nodes already trust the new CAs. Its help text and the `--follow` usage now say so. To start a rotation without the CLI, create the operation with `kubectl` or the UI and hand it off later with `ichor create credentials NAME --acknowledge`.
