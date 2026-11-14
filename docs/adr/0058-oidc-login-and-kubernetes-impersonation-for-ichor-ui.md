# 0058. OIDC login and Kubernetes impersonation for ichor-ui

Status: Accepted · Date: 2026-10-26 · Design section: Trust; CLI and UI
Decided: with `ichorUI.oidc.enabled` (default off) every ichor-ui RPC needs an OIDC ID token that `github.com/coreos/go-oidc/v3` verifies (signature, issuer, audience = client ID, expiry), the backend calls the Kubernetes API as that user with `Impersonate-User`/`Impersonate-Group` so the API server alone decides what they may do, and `AuditRecord.actor` and the PR's `Requested by:` are that Kubernetes user name; the UI is a public client doing authorization code + PKCE in the browser (no client secret exists); the `impersonate` grant is on `users` and `groups` only, rendered only when OIDC is on and narrowable by name; with OIDC off nothing changes except a startup warning and a UI banner; the CLI does not forward credentials

## Context

architecture.md "Trust" says the UI backend holds a read-only Talos identity and the browser holds nothing, and
that "OIDC login with Kubernetes impersonation comes later so that RBAC and audit apply per user". The design
decides nothing more: not the flow, the library, the impersonation scope, the off-state, or the CLI. Until now
`AuditRecord.actor` was the fixed string `ichor-ui`/`ichor-cli`, and anyone who could reach ichor-ui wrote with
its ServiceAccount's permissions (ADR 0030's port-forward is the boundary).

## Decisions

**1. Design gives:** OIDC login, Kubernetes impersonation, RBAC and audit per user, browser holds no kube/Talos
credential. Everything below is what the design left open.

**2. Impersonation.** `uiapi.Server` shadows `Get/List/Create/Update/Delete` so a call made under a context that
carries an `Identity` goes through a client with `Impersonate-User` and one `Impersonate-Group` per group; no
handler changed. Kubernetes then enforces RBAC, and the API server's own audit log names the user. Ichor writes
no authorization logic. Reads that are the backend's own business and whose result is not returned to the
user (secrets bundle, `argocd-cm`, the repository token, the Application that `planWrite` inspects) stay on the
ServiceAccount via `s.Client`: users are not meant to read the secrets bundle. A read whose result reaches the
user (the tracking Application's status, the Kubernetes Node's status, `TalosStorage`) is made as the user;
ADR 0062's amendment lists every `s.Client` read and which side it is on. Without an identity (point 4) the
ServiceAccount is used for all of them. The Talos read-only identity is unchanged; RPCs that reach a node first read the
`TalosMachine` as the user, so a user who cannot `get` it cannot reach its node data.

**Scope of `impersonate`.** One rule: verb `impersonate` on `users` and `groups` in the core group, cluster
scoped (impersonation is a cluster-scoped verb). Not `serviceaccounts`, not `userextras`, not `uids`. It is
rendered only when `ichorUI.oidc.enabled`, so the default install gains nothing. Names cannot be enumerated for
a real IdP, so by default it allows any name; `ichorUI.oidc.impersonate.users`/`.groups` turn into
`resourceNames` for sites that can list them (a user or group outside the list gets a 403). Compensating
controls in the backend: names get a configurable prefix (default `oidc:`) so an IdP value cannot be a built-in,
and a name that still starts with `system:` is refused. The ServiceAccount token remains the thing to protect;
this grant is the reason it matters more once OIDC is on.

**3. Token verification.** `github.com/coreos/go-oidc/v3` v3.21.0 is added. The module graph holds only
`coreos/go-oidc v2.5.0+incompatible` (an old indirect dep of `k8s.io/apiserver`, unusable for a v3 import path)
and no JWKS-verifying code we could reuse without pulling in the apiserver authenticator machinery. It brings
`go-jose/v4`, already in the module graph. The verifier does discovery, JWKS rotation, and checks signature,
issuer, audience and expiry; we add: username claim present (default `email`, which must be `email_verified`),
groups claim optional. Errors say "invalid ID token" and never include the token or library detail.
Discovery failing at startup is fatal, never a silent fall back to open.

**4. OIDC not configured.** Behaviour is exactly as before: writes use the ServiceAccount, the actor is
`ichor-ui`. It is not made stricter, so nobody is locked out on upgrade. What changes is that it is no longer
silent: ichor-ui logs a WARNING at start and `GET /auth/config` reports `enabled:false`, which the UI shows as a
banner. `/auth/config` (issuer and client ID only), the SPA assets and `/metrics` are open; every Connect RPC
needs a token when OIDC is on.

**5. CLI.** The CLI runs on the user's kubeconfig (ADR 0049) and does not forward its credentials to ichor-ui:
sending a kubeconfig token to a server that then acts with it is a credential-forwarding path we do not want, it
would need `tokenreviews` to verify, and a client-certificate kubeconfig has no token to send. With OIDC on the
CLI therefore uses its in-process fallback (the same `uiapi.Server` on the user's own RBAC; actor stays
`ichor-cli` for now). Detection of an OIDC-enabled backend and the actor name are point 5a.

**5a. Amendment: the CLI detects OIDC.** Right after the port-forward is up and
the probe answers, the CLI reads `GET /auth/config`. `enabled:true` makes it drop the tunnel and use the
in-process `uiapi.Server` on the user's own kubeconfig and RBAC, printing one line to stderr saying so. Any other
answer (non-200, unparseable body, an older backend) counts as not enabled, so the call goes to the backend, which
rejects it itself if it does require a token: detection never opens anything up. `describe node` / `logs node` need the
backend's read-only Talos identity, so with OIDC on they fail with a reason (the backend requires OIDC, the CLI
cannot present a token and never forwards kubeconfig credentials) instead of `Unauthenticated`; the flag that
would carry an ID token is still later. The in-process actor (`AuditRecord.actor`, `Requested by:`, the approval
decision) is the username from a `SelfSubjectReview` made with the user's kubeconfig; if that call fails the
actor stays `ichor-cli`. Only the username is read; no credential is logged or stored.

**6. Login in the UI.** Authorization code + PKCE (S256), state and nonce, in the browser, as a public client:
there is no client secret to store, mount or leak, and the backend stays stateless (no sessions or cookies; it
verifies the bearer ID token on each call). The ID token lives in `sessionStorage` and is sent as
`Authorization: Bearer`; a rejected token restarts login. It is a credential for Ichor only, not for Kubernetes
or Talos. Cost: the IdP must allow CORS for browser discovery and token requests and have the UI's origin as a
redirect URI, and there is no refresh token, so the user logs in again when the ID token expires. A
backend-held session was rejected as more code (cookie signing, CSRF, secret handling) for the same result.

**7. Audit.** `AuditRecord.actor` is `Identity.User`, the exact string given to `Impersonate-User`, so it matches
Kubernetes' audit log; `prmode.SubmitPRRequest.Requester` receives the same value for `Requested by:`.

**8. Secrets.** The ID token and (nonexistent) client secret are never logged, put in errors or written to
audit. Nothing logs a request header.

## Amendment: what an impersonated user needs for the effective config

`GetEffectiveConfig` lists `TalosConfigPatch` and `TalosStorage` as the user (ADR 0062), so a user needs `get`/`list`
on `talosclusters`, `talosmachines`, `talosconfigpatches` and `talosstorages` in the cluster's namespace; the
chart's per-CRD `*-viewer-role` ClusterRoles grant exactly that. Without `talosstorages` list the view fails with
Forbidden for that user. The ServiceAccount's own `list` on `talosstorages` (ADR 0018 amendment) is separate: it
serves `ResolveSystemInputs` and the OIDC-off case.

## Amendment: Ichor's own users and issuer (ADR 0094, Proposed)

ADR 0094 adds `IchorUser` accounts and makes ichor-ui an OIDC issuer of its own that runs beside the external IdP
described here. Nothing in this ADR changes while `ichorUI.users.enabled` stays false. When it is on, the
authenticator accepts tokens from both sources, the kube-apiserver trusts both through Talos's
`KubeAuthenticationConfig`, and the CLI can present an Ichor-issued token to the backend (point 5a's "later").
