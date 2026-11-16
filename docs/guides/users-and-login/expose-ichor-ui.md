---
sidebar_order: 3
---

# Expose ichor-ui over https

ichor-ui never serves plain HTTP to the network. One listener on
port 8443 serves the UI, the API, `/metrics` and the probes over TLS, with one
of two certificates:

- The pair you supply with `ichorUI.tls.secretName` (or `--ui-tls-secret`), for
  example a cert-manager Secret. Browsers trust it if the issuer is public.
- Otherwise a leaf from the Ichor serving CA (Secret `ichor-oidc-tls`, issued for
  every install). Its names are the Service ClusterIP, `ichor-ui-service.ichor-system.svc` (`ichor-ichor-ui-service...` on an `ichor init` install)
  (and `.cluster.local`), `localhost` and, when users are on, the host of the
  external URL. Browsers do not trust it, so an edge that terminates the
  browser's TLS has to verify it, and a client that talks to it directly needs
  the CA.

ichor-ui serves HTTPS only, on port 8443. Without `--ui-tls-secret` it uses a
leaf from the Ichor serving CA.

> [!WARNING]
> A browser does not trust that leaf. An edge that terminates the browser's TLS
> has to verify it instead; see [Pick a recipe](#pick-a-recipe).

Making ichor-ui reachable at the `--ui-url` (a LoadBalancer, an Ingress or a
Gateway) is your job: init installs none of them.

## Certificate renewal

ichor-ui reloads its certificate files when they change, within about ten
seconds of the file changing, without a restart: a `kubernetes.io/tls` Secret
that cert-manager renews works as is. The kubelet copies a changed Secret into
the pod on its own schedule, so the new certificate reached the wire 25 to 46
seconds after cert-manager wrote the Secret in a test (cert-manager v1.21.3,
pod restart count 0). If a new pair is invalid, ichor-ui keeps serving the old
certificate and logs the error.

## The Ichor serving CA

The controller publishes the public part of the Ichor serving CA, never a key,
as the ConfigMap `ichor-ui-ca` and the Secret `ichor-ui-ca` in `ichor-system`
The controller publishes the public part of the Ichor serving CA, never a key,
as the ConfigMap `ichor-ui-ca` and the Secret `ichor-ui-ca` in `ichor-system`
(key `ca.crt`). Service port 8443 is named `https`.

## Pick a recipe

Each recipe is on its own page:

- [Ingress-NGINX](expose-ichor-ui/ingress-nginx.md), which re-encrypts to the backend.
- [Gateway API](expose-ichor-ui/gateway-api.md), with a `BackendTLSPolicy`.
- [A load balancer that passes TLS through](expose-ichor-ui/load-balancer.md), which needs your certificate.

Pick one recipe. The names in the recipes are for the Helm chart install
(`ichor-ui-service`). An `ichor init` install and a kustomize install prefix them:
the Service is `ichor-ichor-ui-service` (checked on a cluster made by `ichor init`,
with the same prefix in the leaf's names). Use that name for `name:` and for the
verified host name in the recipe.

## Your own certificate

If you supplied your own certificate, the edge verifies your CA instead of
`ichor-ui-ca`, and the name it checks must be in your certificate. The `ichor`
CLI and other clients inside the cluster are not affected: ichor-ui answers a
handshake that asks for the Service name (SNI `<service>.<namespace>.svc`) with
Ichor's own certificate, so they still verify it against `ichor-ui-ca`. Every
other name and address gets your certificate (2026-11-03).

## Port 8080 and migration

A new install serves nothing on 8080. ichor-ui no longer serves plain HTTP at
all. An install that served plain HTTP before is moved to
HTTPS on 8443 by `ichor upgrade ichor`, which prints "ichor-ui no longer serves
plain HTTP; point your edge at https://...:8443 (trust ichor-ui-ca)". Point the
edge at 8443 and trust `ichor-ui-ca` (see above). The `--http-addr` flag is
accepted for one more release as a no-op that logs "ignored", so a
hand-edited Deployment still starts; the chart value `ichorUI.plaintextPort`
is gone. An install that served TLS on 8080 keeps `--addr` pinned to 8080. See [Install Ichor with Helm](../install-with-helm.md)
for the chart values.

Port 8444 is an internal listener. The kube-apiserver uses it to fetch the
login keys. Never route it from an edge.
