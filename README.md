# Ichor

Manage [Talos Linux](https://www.talos.dev/) clusters from Kubernetes.

Ichor turns nodes, machine configuration, upgrades and etcd backups into
Kubernetes resources. You work with them through a CLI, a web UI, or plain
`kubectl`.

> **Status: alpha.** Exercised end to end on Talos v1.14 test clusters, not yet
> on bare metal. Expect rough edges, and please open an issue when you find one.

## What it does

- **Adds nodes.** Discovers a Talos node in maintenance mode, lets you approve
  it in the UI or CLI, installs Talos and joins it to the cluster.
- **Keeps configuration declarative.** Machine configuration is a set of typed
  Kubernetes resources built on the Talos machinery types. Ichor renders and
  applies them and reports drift.
- **Rolls out changes safely.** Talos and Kubernetes upgrades and
  reboot-requiring config changes go one node at a time, control planes first,
  with an etcd check before each step.
- **Takes care of operations.** Reboot, shutdown, reset, node removal, etcd
  backups and restores, CA rotation and key rotation are operations you start
  and watch.
- **Manages the CNI.** Installs [Cilium](https://cilium.io/) as the cluster's
  CNI and keeps it as a Helm release you can edit and upgrade.
- **Adopts existing clusters.** `ichor adopt` brings a running Talos cluster
  under management.
- **Stays small.** Ichor runs its own controller, UI and Cilium, nothing else.
  Storage, monitoring and GitOps tooling are yours to choose.

## Components

| Component | What it does |
|---|---|
| `ichor` | The CLI. Creates the first node, adopts existing clusters, and does everything the UI does. |
| `ichor-controller` | Reconciles the custom resources. The only component that talks to Talos nodes. |
| `ichor-ui` | The web UI and its backend. Changes things only by writing custom resources. |

## Quick start

You need one machine booted from a Talos ISO into maintenance mode. Install the
`ichor` CLI from a release, then turn the machine into the first control plane
of a new cluster:

```sh
VERSION=0.1.0
OS=$(uname -s | tr A-Z a-z)                                   # linux or darwin
ARCH=$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
BASE=https://github.com/maeshinshin/ichor/releases/download/v${VERSION}

curl -fLO "${BASE}/ichor_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fLO "${BASE}/ichor_${VERSION}_checksums.txt"
sha256sum --check --ignore-missing "ichor_${VERSION}_checksums.txt"   # macOS: shasum -a 256 --check --ignore-missing
tar -xzf "ichor_${VERSION}_${OS}_${ARCH}.tar.gz" ichor
install -m 0755 ichor ~/.local/bin/ichor

ichor init \
  --cluster demo \
  --node-endpoint 192.168.1.50:50000 \
  --endpoint-type node --endpoint-address 192.168.1.50 \
  --install-disk /dev/sda \
  --preset single

ichor cluster get demo
```

Or, on a cluster that already runs Talos, install Ichor with Helm:

```sh
helm repo add ichor https://ichor.maesh.dev/charts
helm install ichor ichor/ichor --version ${VERSION} \
  --namespace ichor-system --create-namespace
```

[Getting started](docs/guides/getting-started.md) walks through every step,
including the download and checksum check, adding more machines and signing in
to the UI. `ichor <command> --help` documents the rest. To build the CLI and
the images yourself, see [Build from source](docs/development/build-from-source.md).

## Documentation

The docs are published at <https://ichor.maesh.dev/>. In the
repository:

- [Getting started](docs/guides/getting-started.md)
- [Adopt an existing cluster](docs/guides/adopt-and-reclaim.md)
- [Install Ichor with Helm](docs/guides/install-with-helm.md)
- [Users and login](docs/guides/users-and-login.md)
- [Hardening](docs/guides/hardening.md)
- [Rotating keys](docs/guides/rotate-keys.md)
- [Build from source](docs/development/build-from-source.md)
- [Design](docs/design/architecture.md)

## Security

Approving a node talks to Talos's maintenance-mode API, which does not
authenticate the node at the other end, so keep new nodes on a separate
provisioning network. Only the controller holds an admin Talos identity and
writes to nodes. The browser holds no credentials. The secrets bundle, the
root of trust of a cluster, lives in a Secret and in an encrypted file on your
machine. The design document has the full picture.

## Contributing

Contributions are welcome. Everything builds and tests inside `nix develop`;
see [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and conventions.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
