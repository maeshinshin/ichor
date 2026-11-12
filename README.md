# Ichor

Manage [Talos Linux](https://www.talos.dev/) clusters from Kubernetes. Nodes,
machine configuration, upgrades and etcd backups are Kubernetes resources, which
you work with through a CLI, a web UI or plain `kubectl`.

> **Status: alpha.**

## Quick start

You need one machine booted from a Talos ISO into maintenance mode.

1. Choose the release and your platform:

   ```sh
   VERSION=0.2.4
   OS=linux      # linux or darwin
   ARCH=amd64    # amd64 or arm64
   BASE=https://github.com/maeshinshin/ichor/releases/download/v${VERSION}
   ```

2. Download the CLI and its checksums:

   ```sh
   curl -fsSLO "${BASE}/ichor_${VERSION}_${OS}_${ARCH}.tar.gz"
   curl -fsSLO "${BASE}/ichor_${VERSION}_checksums.txt"
   ```

3. Verify the download (on macOS use `shasum -a 256 -c -`):

   ```sh
   grep " ichor_${VERSION}_${OS}_${ARCH}.tar.gz\$" "ichor_${VERSION}_checksums.txt" | sha256sum -c -
   ```

4. Install it:

   ```sh
   tar -xzf "ichor_${VERSION}_${OS}_${ARCH}.tar.gz" ichor
   install -m 0755 ichor ~/.local/bin/ichor
   ```

5. Turn the machine into the first control plane of a new cluster:

   ```sh
   ichor init --cluster demo \
     --node-endpoint 192.168.1.50:50000 \
     --endpoint-type node --endpoint-address 192.168.1.50 \
     --install-disk /dev/sda --preset single
   ```

6. Check it:

   ```sh
   ichor cluster get demo
   ```

### Already have a Talos cluster?

Install Ichor with Helm, then register the cluster with `ichor adopt`:

```sh
helm repo add ichor https://ichor.maesh.dev/charts
helm install ichor ichor/ichor --version 0.2.4 \
  --namespace ichor-system --create-namespace
```

Conditions:

- Kubernetes 1.30 or later and a working pod network.
- Cilium, a talosconfig and a secrets bundle are not needed to install.
- Ichor manages no nodes until you run `ichor adopt`.
- On a CNI other than Cilium, adopt needs `--cni-unmanaged`; the CNI is left as it is.

Details: [Install Ichor with Helm](docs/guides/install-with-helm.md).

## Documentation

Published at <https://ichor.maesh.dev/>; in the repository:

- [Getting started](docs/guides/getting-started.md)
- [Install Ichor with Helm](docs/guides/install-with-helm.md)
- [Adopt an existing cluster](docs/guides/adopt-and-reclaim.md)
- [Users and login](docs/guides/users-and-login.md)
- [Security](docs/concepts/security.md) and [Hardening](docs/guides/hardening.md)
- [Overview and components](docs/index.md), [Design](docs/design/architecture.md)
- [Build from source](docs/development/build-from-source.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
