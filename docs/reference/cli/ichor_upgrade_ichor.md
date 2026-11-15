---
title: "ichor upgrade ichor"
description: "Upgrade Ichor itself (IchorInstallation.spec.version)"
---

## ichor upgrade ichor

Upgrade Ichor itself (IchorInstallation.spec.version)

### Synopsis

Upgrade Ichor itself (IchorInstallation.spec.version).

Without VERSION it uses the recommended version (see `--list`). It shows which nodes restart
and in which order, and asks for confirmation unless --yes.

The ichor-ui this installs does not start without a sign-in. If yours has none (an install made before
sign-in became the default), the upgrade first turns Ichor's own sign-in on, as ichor init does: it creates the
first administrator, switches ichor-ui to sign-in at --ui-url (default https://localhost:8443) and sets
spec.authentication.ichor on each TalosCluster that can carry it (Talos v1.14 or later). That last step marks
every control plane APIServerRestartRequired; kube-apiserver restarts on each control plane only when you run
"ichor rollout config". It asks first (--yes skips the question) and prints the generated password once, at the
end. An Ichor that Helm installed is not touched: set ichorUI.users.enabled or ichorUI.oidc in the chart values.

```sh
ichor upgrade ichor VERSION [flags]
```

### Examples

```sh
  ichor upgrade ichor v0.3.1 --follow
```

### Options

```text
      --admin-user string                 if ichor-ui has no sign-in yet: name of the first administrator (default admin); its generated password is printed once to stderr
      --cluster string                    the cluster (default: the only one in the namespace)
      --follow                            after creating the operation, follow its progress until it finishes
  -h, --help                              help for ichor
      --installer-digest string           pin the installer image to this digest (sha256:...) instead of resolving the version's tag
      --kube-api-server-url string        if ichor-ui has no sign-in yet: https URL of the Kubernetes API put in users' kubeconfigs
      --kube-api-tls-server-name string   if ichor-ui has no sign-in yet: tls-server-name in users' kubeconfigs
      --max-unavailable int32             workers handled at once (default 1)
      --skip-signature-check              run the installer image WITHOUT checking its signature (for builds nobody signs: development and demo images)
      --ui-tls-cert string                if ichor-ui has no sign-in yet: PEM certificate file for its TLS; stored with --ui-tls-key as a Secret
      --ui-tls-key string                 with --ui-tls-cert: PEM private key file (never printed)
      --ui-tls-secret string              if ichor-ui has no sign-in yet: serve it over TLS with this existing kubernetes.io/tls Secret
      --ui-url string                     if ichor-ui has no sign-in yet: the https URL at which browsers and the CLI reach it, the issuer of every token (default https://localhost:8443)
```

### Options inherited from parent commands

```text
      --bundle-passphrase-file string   file holding the passphrase of the local secrets bundle; any readable path, e.g. /dev/fd/3; otherwise $ICHOR_BUNDLE_PASSPHRASE, then a prompt on a terminal. Never a value on the command line
      --ca-file string                  PEM file of the CA that signed ichor-ui's serving certificate, when ichor-ui serves TLS (env ICHOR_CA_FILE)
      --ichor-namespace string          namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives) (default "ichor-system")
      --kubeconfig string               path to the kubeconfig for the cluster's Kubernetes API; defaults to $KUBECONFIG, then ~/.kube/config
      --local                           do not use ichor-ui; compute from the Kubernetes API directly
  -n, --namespace string                namespace of the Ichor resources the command names (default "ichor-system")
  -o, --output string                   output format: "yaml" or "json" for scripts; by default a command prints for people (commands without a human view print YAML)
      --override                        incident only: override a safety check (reset a node, restore etcd, reboot or shut down the last worker, an upgrade the compatibility table refuses); needs the override RBAC verb on the resource; implies --local
      --talosconfig string              path to the talosconfig used to reach each node's Talos API; defaults to $TALOSCONFIG, then ~/.talos/config
      --tls-server-name string          name on ichor-ui's serving certificate, when it is not the Service's DNS name (<service>.<namespace>.svc)
      --yes                             skip interactive confirmation before a dangerous operation
```

### SEE ALSO

* [ichor upgrade](ichor_upgrade.md)	 - Upgrade the cluster's Talos, Kubernetes or Cilium, or Ichor itself

