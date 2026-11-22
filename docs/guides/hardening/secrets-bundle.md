---
sidebar_order: 7
---

# The local secrets bundle

![Where the bundle passphrase comes from and what uses the bundle. The passphrase is read from --bundle-passphrase-file, then ICHOR_BUNDLE_PASSPHRASE, then a terminal prompt or a generated passphrase. It seals secrets.yaml, which holds every CA key and is read by restore cluster, adopt, create credentials --bundle and export bundle. ichor secrets encrypt seals an older plaintext file.](../../images/hardening-secrets-bundle.light.drawio.svg)

*The passphrase seals `secrets.yaml`; the commands that read it take the passphrase the same way.*

`~/.ichor/clusters/<name>/secrets.yaml` holds every CA key of the cluster. It is
an age file sealed with a passphrase, so a copy on a backup or cloud drive is
not a copy of the keys. `ichor init`, `ichor adopt --recover-secrets` and
`ichor export bundle` write it that way by default (`--bundle-plaintext` opts out).

## The passphrase

In order:

1. `--bundle-passphrase-file PATH`: the first line. `PATH` may be a descriptor, so the passphrase never touches disk: `ichor restore ... --bundle-passphrase-file <(pass show ichor/demo)`.
2. `ICHOR_BUNDLE_PASSPHRASE`. Anything running as you, and root, can read it; prefer the file.
3. On a terminal: a prompt to open a file; a generated passphrase, printed once, to create one.

Never a flag value (`ps`, shell history). A chosen passphrase needs 12 or more
characters. A wrong passphrase and a damaged file give the same error.

## Encrypt an older plaintext bundle

A plaintext bundle still works, with a warning on every use:

```sh
ichor secrets encrypt demo              # ~/.ichor/clusters/demo/secrets.yaml
ichor secrets encrypt --file ./secrets.yaml
```

It seals the file, opens the sealed copy, compares it with the original, and
only then replaces the file. `age -d secrets.yaml` opens it without Ichor.

> [!WARNING]
> Encrypting does not undo an earlier copy or exposure of the plaintext: rotate the CAs (`ichor rotate ca`).

## The kubeconfig beside it

The kubeconfig stays plaintext, because `kubectl` reads it. It holds the
`system:masters` override certificate ([Users and login](../users-and-login.md)):
once login works, move it offline, but keep it.

The bundle can also mint new credentials with no kubeconfig on disk:

```sh
ichor create credentials NAME --bundle secrets.yaml --kube-server URL --talos-endpoints ADDRESS
```

The bundle holds no addresses, so note the API server's address with it.

The copy the controller reads is the Secret in the cluster. The commands that
read that Secret (`create credentials`, `export bundle`, the `rotate ca`
hand-off, `adopt --add-machine`) need the Role `ichor-bundle-secret-role`, which
the chart installs when `rbac.bundleSecret.names` lists it. Bind it only to
people you would give `os:admin`.
