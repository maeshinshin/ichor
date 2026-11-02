# Users and login

Ichor can keep its own users, so people can sign in to ichor-ui and get their
own kubeconfig with no shared secret. This guide covers the steps. The design
is in [ADR 0094](../adr/0094-ichor-users-its-own-oidc-issuer-roles-and-per-user-kubeconfigs.md),
which builds on [ADR 0058](../adr/0058-oidc-login-and-kubernetes-impersonation-for-ichor-ui.md)
for logins from an external identity provider.

Status: ADR 0094 is accepted and was verified on hardware. The steps below are
what it decides, plus the CLI flags the implementation added (`--ca-file`,
`--out`, `--bootstrap`).

Users are off by default. Nothing here applies until you turn them on.

## Without any sign-in (ADR 0108)

With neither Ichor users nor an external OIDC provider (`ichorUI.oidc`), ichor-ui
has no way to tell callers apart. It then serves read-only calls only and
refuses every change with "ichor-ui has no sign-in configured"; the UI shows a
"Read-only" banner saying so. Downloads need an identity and are refused too.

The old open mode, where anyone who can reach ichor-ui writes with its
ServiceAccount's permissions and is audited as `ichor-ui`, still exists but you
have to ask for it: `--allow-unauthenticated` on ichor-ui, `ichorUI.allowUnauthenticated=true`
in the chart. The flag does nothing once users or OIDC are on.

- `ichor init` without `--ui-url` keeps its behaviour: it starts ichor-ui with
  the flag and prints a warning at the end. `--ui-url` turns users on and
  drops the flag.
- `ichor-installer` upgrading an install that ran with no sign-in sets the
  flag for you and prints a `NOTICE`, so nothing stops working. Turn users or
  OIDC on, and the flag goes away at the next upgrade.
- A Helm upgrade does not guess: set `ichorUI.allowUnauthenticated=true` to
  keep writing, or enable `ichorUI.users` or `ichorUI.oidc`. `helm` prints a
  note when no sign-in is configured.

## Before you start

Turning users on needs three things:

- An https URL that browsers and the `ichor` CLI can reach. Ichor does not
  provide one.
- A Talos v1.14 contract or later. Older contracts cannot carry the issuer
  configuration.
- A rollout of the control planes with `ichor config apply`. The kube-apiserver
  reads its login settings from the machine config.

## Turn users on

On a new cluster, pass the URL to `ichor init`:

```
ichor init --ui-url https://ichor.example.com --admin-user admin ...
```

`--version-contract` must be v1.14 or later; init refuses the flags before it
touches the node otherwise. `ichor init` installs Ichor from the manifest in
the binary, not from the Helm chart, so it applies in place what
`ichorUI.users.enabled` and `ichorUI.users.externalURL` render: the flags,
the internal port and the key volumes of the ichor-ui Deployment, the internal
port of its Service, and the empty `ichor-user-credentials` Secret. It then
creates the first admin (`--admin-user`, default `admin`, role `admin`) and
sets `spec.authentication.ichor` on the TalosCluster. The order is: Secret,
admin, then the switch, so nobody is locked out. At the end it prints, to
stderr, the Helm values that match and the generated
password of the admin. The password is printed once, also when a later step
failed, and is stored nowhere; the first login forces a change. Save it when it
is printed. The kube-apiserver login settings reach the control planes with
`ichor config apply`, which restarts the kube-apiserver on each control plane
in turn (see the end of this guide). Init retries transient API errors (such as
an etcd timeout right after the API server comes up) for a few minutes before
it gives up.

`ichor init` takes four more flags, all of which need `--ui-url`. They are
checked before anything is written, and each has a chart value:

| `ichor init` flag | Chart value | Effect on ichor-ui |
|---|---|---|
| `--ui-tls-secret NAME` | `ichorUI.tls.secretName` | Mounts the existing `kubernetes.io/tls` Secret `NAME` (in `ichor-system`), sets `--tls-cert-file` and `--tls-key-file`, and probes over HTTPS. |
| `--ui-tls-cert FILE` with `--ui-tls-key FILE` | `ichorUI.tls.secretName` | Alternative to `--ui-tls-secret`: init stores the PEM pair as the Secret `ichor-ui-tls` and uses it the same way. The key is never printed. |
| `--kube-api-server-url URL` | `ichorUI.users.kubeAPIServerURL` | The https URL put in users' kubeconfigs (default: the cluster's `spec.endpoint`). |
| `--kube-api-tls-server-name NAME` | `ichorUI.users.kubeAPITLSServerName` | The `tls-server-name` in users' kubeconfigs, for a URL host outside the kube-apiserver certificate. |

ichor-ui reloads its certificate files when they change, within about ten
seconds of the file changing, without a restart: a `kubernetes.io/tls` Secret
that cert-manager renews works as is. The kubelet copies a changed Secret into
the pod on its own schedule, so the new certificate reached the wire 25 to 46
seconds after cert-manager wrote the Secret in a test (cert-manager v1.21.3,
pod restart count 0). If a new pair is invalid, ichor-ui keeps serving the old
certificate and logs the error.

ichor-ui serves HTTPS only, on port 8443 (ADR 0105). Without
`--ui-tls-secret` it uses a leaf from the Ichor serving CA. A browser does not
trust that leaf; see "Expose ichor-ui over https". Making ichor-ui reachable at
the `--ui-url` (a LoadBalancer, an Ingress or a Gateway) is your job: init
installs none of them.

On an existing install, set `ichorUI.users.enabled` to `true` and
`ichorUI.users.externalURL` to the https URL in the chart values, and create
the first admin before every request starts to need a login:

```
ichor user create admin --role admin --generate --bootstrap
```

`--bootstrap` needs no login, because nobody can log in yet. It uses your own
kubeconfig, so Kubernetes decides: you need `create` on `ichorusers` and `get`
and `patch` on the Secret `ichor-user-credentials`. The CLI creates the
`IchorUser` and writes the argon2id hash of the password into that Secret with
the same code ichor-ui uses. It refuses a name that already exists (it never
resets a password) and a cluster where the Secret does not exist, which means
users are not turned on yet (the chart creates it with
`ichorUI.users.enabled`). The generated password is printed once to stderr and
must be changed at the first login. Instead of `--generate` you can type the
password at a prompt with no echo, or pipe it with `--password-stdin`. Then set
`spec.authentication.ichor` (`enabled: true`, `url`) on the TalosCluster, which
needs `update` on `talosclusters/authentication`, and run `ichor config apply`
if a change waits for a reboot. Once users are on, every request to ichor-ui
needs a login.

## Expose ichor-ui over https

ichor-ui never serves plain HTTP to the network (ADR 0105). One listener on
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

The controller publishes the public part of the Ichor serving CA, never a key,
as the ConfigMap `ichor-ui-ca` and the Secret `ichor-ui-ca` in `ichor-system`
(key `ca.crt`). Service port 8443 is named `https`. Pick one recipe. The names
below are for the Helm chart install (`ichor-ui-service`). An `ichor init`
install and a kustomize install prefix them: the Service is
`ichor-ichor-ui-service` (checked on a cluster made by `ichor init`, with the
same prefix in the leaf's names). Use that name for `name:` and for the verified
host name below.

If you supplied your own certificate, the edge verifies your CA instead of
`ichor-ui-ca`, and the name it checks must be in your certificate.

### Ingress-NGINX (re-encrypt to the backend)

The Ingress must be in `ichor-system`, because it names the Service. Annotations
(Ingress-NGINX annotations reference, "Backend Protocol" and "Backend Certificate
Authentication"):

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ichor-ui
  namespace: ichor-system
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
    nginx.ingress.kubernetes.io/proxy-ssl-verify: "on"
    nginx.ingress.kubernetes.io/proxy-ssl-secret: "ichor-system/ichor-ui-ca"
    nginx.ingress.kubernetes.io/proxy-ssl-name: "ichor-ui-service.ichor-system.svc"
    nginx.ingress.kubernetes.io/proxy-ssl-server-name: "on"
spec:
  ingressClassName: nginx
  tls:
    - hosts: [ichor.example.com]
      secretName: ichor-example-com-tls
  rules:
    - host: ichor.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ichor-ui-service
                port:
                  name: https
```

- `backend-protocol: "HTTPS"` makes NGINX speak TLS to the pod.
- `proxy-ssl-verify` is off by default; without it NGINX does not check the
  pod's certificate.
- `proxy-ssl-secret` takes `namespace/secretName`. The reference docs describe a
  Secret with `tls.crt` and `tls.key` (a client certificate, which ichor-ui does
  not ask for) that "should also contain" `ca.crt`. A Secret with only `ca.crt`,
  which is what `ichor-ui-ca` is, is accepted: tested with Ingress-NGINX chart
  4.15.1 (controller 1.15.1), the request reached ichor-ui and nginx used the CA
  as `proxy_ssl_trusted_certificate`. Pointing the annotation at a Secret that
  holds another CA gives 502 (`upstream SSL certificate verify error`).
- `proxy-ssl-name` overrides the name NGINX verifies; `proxy-ssl-server-name`
  sends it as SNI. Use a name in the certificate. It is required: without it
  NGINX verifies the literal name `upstream_balancer` and every request is a
  502. A name that is not in the certificate is a 502 too (`upstream SSL
  certificate does not match`). Ingress-NGINX takes up to about half a minute
  to reload after an annotation change, so wait before you read a result.
- The public certificate for browsers is the Ingress's own `tls` Secret.
- If you supplied your own certificate, point `proxy-ssl-secret` at a Secret that
  holds its CA as `ca.crt`.

### Gateway API: BackendTLSPolicy

`BackendTLSPolicy` is `gateway.networking.k8s.io/v1`, in the Standard channel
since Gateway API v1.4 (it was `v1alpha3` before). Your Gateway implementation
must support it, and the CRDs installed in the cluster must be v1.4 or later.
Release 1.4.1 notes say a policy supports a single `targetRefs` entry.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: BackendTLSPolicy
metadata:
  name: ichor-ui
  namespace: ichor-system
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: ichor-ui-service
  validation:
    hostname: ichor-ui-service.ichor-system.svc
    caCertificateRefs:
      - group: ""
        kind: ConfigMap
        name: ichor-ui-ca
```

- It lives in the Service's namespace; `caCertificateRefs` may name only objects
  in the policy's own namespace. That is why Ichor publishes `ichor-ui-ca` in
  `ichor-system`.
- The Core-support reference is one ConfigMap with the key `ca.crt`, which is
  what `ichor-ui-ca` is. A Secret reference is implementation-specific.
- `validation.hostname` is the SNI and the name the certificate must match.
- The HTTPRoute that sends traffic to the Service references port 8443 (named
  `https`). A route in another namespace needs a `ReferenceGrant`.
- Check the policy's `Accepted` and `ResolvedRefs` conditions.
- Tested with Envoy Gateway v1.9.2 (its chart installs the Gateway API v1.6.1
  CRDs, `BackendTLSPolicy` served as `v1`): the policy was `Accepted` and
  `ResolvedRefs`, requests reached ichor-ui, a ConfigMap with another CA or a
  `hostname` that is not in the certificate gave 503 from Envoy, and without the
  policy Envoy spoke plain HTTP to port 8443 (ichor-ui answered 400). Envoy took
  from 15 s to a minute to apply a change, so wait and repeat the request. A
  Gateway needs a `listeners[].hostname` (singular), not `hostnames`.

### Load balancer passthrough (operator certificate)

A `Service` of type `LoadBalancer` (`ichorUI.service.type: LoadBalancer`) or a
TCP passthrough terminates nothing, so clients see ichor-ui's own certificate
and the name they typed. Supply your certificate:

```
ichor init --ui-tls-secret ichor-ui-cert --ui-url https://ichor.example.com ...
# or in the chart
helm upgrade ichor ... --set ichorUI.tls.secretName=ichor-ui-cert
```

The Secret is `kubernetes.io/tls` and its certificate must cover the name
clients use. Without it, only clients given the Ichor CA verify:
`ichor --ca-file ca.crt ...` (or `ICHOR_CA_FILE`, from
`kubectl -n ichor-system get configmap ichor-ui-ca -o jsonpath='{.data.ca\.crt}'`).
An L7 cloud load balancer that cannot verify its backend is out of scope: use a
controller that can.

### Port 8080 and migration

A new install serves nothing on 8080. ichor-ui no longer serves plain HTTP at
all (ADR 0105, unit 10). An install that served plain HTTP before is moved to
HTTPS on 8443 by `ichor upgrade ichor`, which prints "ichor-ui no longer serves
plain HTTP; point your edge at https://...:8443 (trust ichor-ui-ca)". Point the
edge at 8443 and trust `ichor-ui-ca` (see above). The `--http-addr` flag is
accepted for one more release as a no-op that logs "ignored", so a
hand-edited Deployment still starts; the chart value `ichorUI.plaintextPort`
is gone. An install that served TLS on 8080 keeps `--addr` pinned to 8080. See
"Install Ichor with Helm" for the chart values.

Port 8444 is an internal listener. The kube-apiserver uses it to fetch the
login keys. Never route it from an edge.

## Create users and roles

Each user is an `IchorUser` object in `ichor-system`. You can create one with
the CLI or apply one yourself. Its name is the login name and cannot change. It
has:

- a display name and an e-mail (for information only);
- `roles`, a list of role names;
- `disabled`, which is false for a new user.

The built-in roles are:

| Role | Can |
|---|---|
| `viewer` | read clusters, machines, operations and nodes |
| `operator` | viewer, plus edit machines and config, and start routine operations |
| `admin` | operator, plus delete, user administration, sign-in settings and the destructive operations |

A role is a group in Kubernetes. A custom permission is a ClusterRole bound to
the group `ichor:<name>` of the user. No built-in role can read secrets.

A new user has no password until an admin sets one. The admin can set a
password, or ask for a generated one, which is shown once. The user changes it
at the next login. A password is 12 to 128 bytes long, with no other rules.

Users can change their own password in the account menu.

### The `ichor user` commands

They talk to ichor-ui directly at the issuer URL over https, with your login (a
token for ichor-ui, from the browser sign-in; the first command opens it). They
do not use a port-forward and need no kubeconfig, so they work from a laptop.
Give the URL with `--issuer` or `ICHOR_ISSUER`, and `--ca-file` (or
`ICHOR_CA_FILE`) when a private CA signed ichor-ui's certificate. Output is
`-o yaml|json`.

```
ichor user list
ichor user get alice
ichor user create alice --display-name "Alice" --role viewer --generate
ichor user update alice --role operator        # only the flags you pass change
ichor user update alice --disabled=true
ichor user set-password alice --password-stdin
ichor user revoke-sessions alice
ichor user delete alice                        # asks; --yes skips
```

`create` and `set-password` take the password from a prompt with no echo (asked
twice), from the first line of stdin with `--password-stdin`, or generate one
with `--generate`; a generated password is printed once to stderr. A password is
never a flag value. The password set by an admin is marked must-change.
Kubernetes RBAC decides who may do what: the admin role for everything,
`update` on `ichorusers/password` for `set-password` and `revoke-sessions`.

## Sign in and get a kubeconfig

To sign in from the terminal:

```
ichor auth login --issuer https://ichor.example.com --audience ichor-ui
```

This opens a browser for the login page. The password is typed only into that
page; the CLI never sees it. The default audience is `ichor-kube`, the login
`kubectl` needs; `--audience ichor-ui` is the one the `ichor user` commands use.
`ichor auth status` shows who you are and when the
cached tokens expire; it prints no token. `ichor auth logout` removes the local
login cache. If the certificate of ichor-ui comes from a private CA, add
`--ca-file ca.pem` (or `export ICHOR_CA_FILE=ca.pem`) to every `ichor auth`
and `ichor user` command.

To get a kubeconfig:

```
ichor auth kubeconfig --issuer https://ichor.example.com --out ~/.kube/ichor.yaml
```

`--out -` prints it to stdout; without `--out` the file is named as ichor-ui
suggests and written to the current directory. The file is written with mode 0600 and the
command refuses to overwrite an existing file. With `--ca-file ca.pem`, the
kubeconfig carries `--ca-file <absolute path>` in the `ichor auth token`
arguments, so `kubectl` trusts the same CA; without the flag the arguments do
not change.

You can also use "Download my kubeconfig" in the account menu of ichor-ui. The
file holds the API server address and the cluster CA. It holds no secret. When
`kubectl` runs, it calls `ichor auth token`, which uses the login cache. You
need at least the `viewer` role to download it.

The login cache is a credential on your machine. It is stored in
`~/.ichor/auth/` with restricted permissions.

After a Kubernetes CA rotation, a kubeconfig downloaded earlier fails TLS. Get
a new one.

## What a sign-in is, and what ends it

- An ID token lasts 5 minutes by default (1 minute to 1 hour). ichor-ui checks
  it against the user's `notBefore` on every request: **revoking sessions or
  changing the password ends tokens issued before it**, here within 10 seconds
  per replica (a cache), and a browser that changes its own password is signed
  out. The kube-apiserver cannot do this check, so a token already given to
  `kubectl` lives out its 5 minutes there.
- A refresh token (CLI only) is single-use: every refresh returns a new one
  and retires the old. Presenting a retired token ends the whole chain, which is
  what a stolen copy looks like; the owner signs in again. The state is kept
  in the user's entry of the `ichor-user-credentials` Secret, so all replicas
  agree, and it never extends the session: the chain dies at the original
  lifetime. Tokens issued before the upgrade that added this are accepted once
  and then rotated.
- Log and download streams end with "your sign-in expired" when the token does.
- Wrong passwords slow down, they do not lock out. The counter is per
  (client address, user name), so a stranger cannot lock `admin` out for you.
  After 5 failures that pair waits 1 s, 2 s, 4 s and so on up to 15 minutes;
  one address waits too after 30 failures across names (up to 1 minute), and
  once a user has 30 failures within an hour from any addresses, every
  sign-in of that user takes 2 s longer (never refused). Counters are kept per
  replica, so with N replicas the free guesses are N times as many.
  Behind a proxy or load balancer the client address is the proxy's unless you
  list it in `ichorUI.users.trustedProxyCIDRs` (`--trusted-proxy-cidrs`), when
  the rightmost `X-Forwarded-For` address outside the list is used. Password
  hashing for sign-in has its own queue, so a flood of sign-ins gets `503
  Retry-After` and cannot block a password change.
- A user may grant only roles they hold (admin: any); Kubernetes RBAC alone
  used to decide this.

## Disable a user

Set `disabled: true` on the `IchorUser`, through the CLI or with kubectl. The effect
has two parts:

- In ichor-ui, the change applies within 10 seconds.
- For `kubectl`, the change applies at the next token refresh, at most 5
  minutes later.

Ichor has no way to take back a token that is already issued. The 5 minute
token lifetime is the limit.

To end every existing session of a user, revoke their sessions. Changing a
user's password also ends their earlier sessions. A session lasts at most 12
hours.

## Break-glass

Ichor keeps a break-glass credential that does not depend on login. It is the
`admin` client certificate in `~/.ichor/clusters/<name>/kubeconfig`, and the
Talos `os:admin` identity in the same folder. If ichor-ui is down, the issuer is
broken, or every user is locked out, use this credential.

Once users are on, keep this folder offline and use it only in an incident.
Do not confuse it with the break-glass write in
[ADR 0065](../adr/0065-break-glass.md), which is an RBAC verb that guards destructive operations.

## Control plane rollout

Talos applies the login settings without a reboot, but restarts the
kube-apiserver on each change (15 to 45 seconds of `/readyz` down per control
plane). The machine controller does not apply the change by itself: the
machines report `APIServerRestartRequired`, and `ichor config apply` (or Apply configuration on the UI's Overview) rolls it out one control plane at a time with
no drain, waiting for that node's `/readyz` before the next. On a cluster with
one control plane the API is unavailable while it restarts.
