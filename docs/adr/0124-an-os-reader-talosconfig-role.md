# 0124. An os:reader talosconfig role

Status: Accepted · Date: 2026-11-04 · Design section: Credentials, Security · Touches: 0058, 0084, 0093, 0094, 0108

Decided: a person with the Kubernetes RBAC verb `download` on `talosclusters/talosconfig-reader` gets a short-lived talosconfig whose client certificate carries the Talos role `os:reader`, for `talosctl`. It is a new `TalosCredentialRequest` purpose, `talosconfig-reader` (default lifetime 1 hour, cap 12 hours, longer is refused at admission and again by the controller), signed by the controller for a key the requester keeps. Every issuance is recorded as an Event on the TalosCluster that names the requesting user, written by the admission webhook before the request is admitted (a request whose Event cannot be written is refused). `ichor cluster talosconfig NAME` and the UI (the cluster page's "..." menu, a download) are the two ways to ask. The shipped roles `ichor-operator` and `ichor-admin` carry the verb; `ichor-viewer` does not. Ichor offers no `os:operator` talosconfig.

## Context

ADR 0108 left "an `os:reader` talosconfig role" in its list of options that are not modelled. What exists today:

- `ichor cluster credentials --read-only` and the UI credentials download (`readOnly`) hand out an `os:reader` talosconfig, but both are the CA-rotation hand-off (ADR 0091, 0093): the CLI reads the secrets bundle with the caller's own Kubernetes rights and signs locally, so the caller needs `get secrets` (the CA keys); the UI path needs `download` on `talosclusters/credentials`, which also gates the cluster-admin kubeconfig, and its default lifetime is a year.
- The purposes `node-reader`, `support-bundle`, `etcd-snapshot` and `packet-capture` are 10 to 15 minute identities for one Ichor call; nothing hands them to a person.

So a person who only needs to look at a node with `talosctl` (`get members`, `logs`, `dmesg`, `processes`, `get` of non-secret resources) has to be given either the secrets bundle or the cluster-admin download. Neither is a good grant for read access.

Talos role facts, from the Talos v1.14 RBAC page (https://docs.siderolabs.com/talos/v1.14/security/rbac): the roles are `os:admin` (every API method), `os:operator` (everything the reader may do plus operational methods such as reboot, shutdown and etcd backup), `os:reader` ("safe" methods: it can list files but not read their contents), `os:etcd:backup` (only `EtcdSnapshot`). "The certificate subject's organization field is used to encode user roles": the role is the `O=` of the client certificate, which is what `credentials.IssueTalos` writes from `role.Set` and what the test below reads back. RBAC is on by default for clusters created with `talosctl` since v0.11 and in Ichor's rendered machine config; `talosctl config info` shows the roles of a talosconfig. The hardware run of ADR 0108 showed that an `os:reader` talosconfig answers `talosctl version` and is refused (`PermissionDenied`) for `get machineconfig`.

## Decision

1. **A new purpose, `talosconfig-reader`.** An enum value added to `TalosCredentialRequest.spec.purpose` (append-only; no field changes). The certificate is Talos `os:reader`, Ed25519, signed like the other operator credentials: the controller loads the rotation state, so during a CA rotation the certificate comes from the new OS CA and the trust bundle holds the old and the new one and a talosconfig issued in the cut-over hour keeps working. `credentials-talos` with `talosRole: reader` stays for the rotation hand-off; it is a different grant on purpose (`credentials` also hands out cluster-admin Kubernetes access).
2. **Lifetime.** Default 1 hour, cap 12 hours (a working day; `credentials.TalosconfigLifetime`, `MaxTalosconfigLifetime`). `PurposeLifetime` refuses more. The webhook refuses a longer `spec.ttl` at admission, before anything is signed or recorded; the controller checks again. There is no renewal: asking again makes a new certificate (and a new Event).
3. **Who may ask.** The webhook's SubjectAccessReview as the requester is `download` on `talosclusters/talosconfig-reader` (name: the cluster). `download` is the verb of ADR 0084 and 0093 for files a person takes away, so no new verb; `issue` stays the verb of ichor-ui's own ServiceAccount purposes. The requester is the signed-in person: the CLI uses the user's kubeconfig; the UI creates the request as the impersonated user, as the other downloads do (ADR 0108 section 1), so the SAR and the audit log name that person and a compromised backend cannot mint one for someone who does not hold the verb.
4. **Mapping of Ichor roles (ADR 0058, 0094).** `ichor-admin` and `ichor-operator` hold `download` on `talosclusters/talosconfig-reader`, plus what the CLI needs to ask and read the answer (`create`, `get` on `taloscredentialrequests`; `ichor-admin` already had `create`). `ichor-viewer` holds nothing new. Minimal privilege: a viewer can already read what the UI shows, but a talosconfig is the node's own API (logs of every service, kernel log, process and connection tables, resource reads) without Ichor's filtering and masking, and it is reachable from the viewer's workstation. Granting it to a viewer is one binding of the cluster-scoped verb (`ichorUI.credentials` style, documented in the guide), so the default is the narrower one. The custom roles of ADR 0094 are unaffected.
5. **An Event per issuance.** The validating webhook, after the SAR passes, creates a `Normal` Event, reason `TalosconfigReader`, on the TalosCluster, with the message `an os:reader talosconfig was requested by <user> (lifetime <d>)`, using the controller's own client. If the Event cannot be created the request is refused ("a credential that cannot be recorded is not issued", the rule of ADR 0084's downloads). A dry-run request writes none. The user name is the Kubernetes user of the request (`ichor:alice` for an Ichor user, the OIDC name otherwise). Not in the Event: the certificate (public anyway), the key (never leaves the requester) and a serial (the certificate does not exist at admission). The request object itself lives two minutes (ADR 0108).
6. **No `os:operator` talosconfig.** An operator certificate lets `talosctl` reboot, shut down, upgrade and reset a node and take etcd snapshots, outside the `TalosOperation` path that the controller serializes, plans and audits (the controller is the only writer to Talos nodes). The packet capture purpose already hands the operator role to Ichor itself for 15 minutes, bound to one call. Operators who really need `talosctl reboot` have the admin talosconfig of `ichor cluster credentials`.
7. **How a person asks.**
   - CLI: `ichor cluster talosconfig NAME [--ttl 1h] [--out FILE] [--talos-endpoints ...]` creates a `TalosCredentialRequest` with the caller's kubeconfig, keeps the key in memory, polls its status, and writes a talosconfig (mode 0600; endpoints from the cluster's control-plane machines, else `--talos-endpoints`, else the current talosconfig's). It never overwrites the current talosconfig: the default file is the current talosconfig's path plus `.ichor-reader`. It needs no `get secrets`.
   - UI: `DiagnosticsService.DownloadReaderTalosconfig` (a server stream of the file like the other downloads); `GetMyPermissions` answers `talosconfig_reader` for the cluster so the "Download read-only talosconfig" item in the Overview page's "..." menu appears only for people who hold the verb. The UI offers 1 hour, 4 hours and 12 hours.

## Rejected

- **Reusing `credentials-talos` with `talosRole: reader` and `download` on `talosclusters/credentials`.** That subresource also gates the cluster-admin kubeconfig, and its lifetime default is a year (the rotation hand-off must outlive the rotation).
- **Signing in the CLI from the secrets bundle (as `--read-only` does).** It needs `get secrets` on the bundle, i.e. every CA key, to hand out the least privileged file.
- **Giving every viewer the verb.** See Decision 4.
- **An `os:operator` option.** See Decision 6; it would widen what a person can do to a node beyond the CR path.
- **The user's name in the certificate.** The webhook is a validating webhook and the controller does not see the requester; stamping the name would need a mutating webhook and trust in an annotation. The Event and the API server's audit log carry the name.
- **A longer default.** A talosconfig is a bearer credential on a laptop; one hour is enough for an investigation, and asking again is one command.

## Risks

- The talosconfig is a bearer credential until it expires; revocation is not possible (Talos has no CRL), so the cap is the control. A cluster whose CA rotates keeps the file valid until expiry only if it trusts the old CA, which the dropped-CA stage ends.
- The Event is written at admission, not after the controller signed: a request the controller then fails (cluster missing, bundle unreadable) still has its Event. That is conservative for an audit.
- `os:reader` is a statement about Talos' API surface, not about secrets: it can read kernel logs and service logs, which may contain sensitive text. That is why the default role set leaves viewers out.

## Test

- `internal/credentials`: `TestPurposeLifetime` covers the new purpose (1 h default, 12 h cap, 12 h + 1 s refused); `TalosRoles` maps it to `os:reader` only.
- `internal/controller` (envtest): the issued certificate's subject `O=` is exactly `os:reader`; it verifies against `status.trust`; `NotAfter` is about one hour; 13 hours fails with no certificate.
- `internal/webhook/v1alpha1`: a holder of `download talosclusters/talosconfig-reader` is admitted and an Event naming the user exists; a user without it, a user with only `talosclusters/credentials`, and the backend's ServiceAccount are refused with no Event; 13 hours is refused; a dry-run writes no Event; an Event that cannot be written refuses the request.
- `internal/cli`: `ichor cluster talosconfig` writes a mode-0600 talosconfig with the controller's certificate and the key made locally, refuses to overwrite the current talosconfig and a `--ttl` above 12 h.
- `internal/uiapi`: the stream carries a parseable talosconfig whose certificate is `os:reader`; a caller without the verb gets `PermissionDenied`; `GetMyPermissions` answers the flag.
- `ui`: the menu item shows only with the flag.
- RBAC: `TestBuiltinRolesRunTheDocumentedFlows` lists the two flows (CLI and UI); operator and admin run them, viewer does not.

## Hardware verification (later)

1. As an Ichor operator (`ichor auth login`), `ichor cluster talosconfig <cluster> --ttl 1h --out /tmp/reader.yaml`; `talosctl --talosconfig /tmp/reader.yaml config info` shows `Roles: os:reader` and an expiry of about 1 hour.
2. `talosctl --talosconfig /tmp/reader.yaml get members` and `talosctl ... dmesg | head`, `talosctl ... logs kubelet` work.
3. `talosctl --talosconfig /tmp/reader.yaml reboot` (and `get machineconfig`) answer `PermissionDenied ... not authorized`; nothing reboots.
4. `kubectl -n ichor-system get events --field-selector reason=TalosconfigReader` shows the user's name for each of the above.
5. As a viewer, the same command is refused by the webhook naming the verb; `--ttl 13h` is refused for everyone.
6. In the UI as an operator, Overview, "...", Download read-only talosconfig; the file works as in 2 and 3. A viewer does not see the item.
7. During a CA rotation (AwaitingCredentials), a new reader talosconfig works as in 2.
