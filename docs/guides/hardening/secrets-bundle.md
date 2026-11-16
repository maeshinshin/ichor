---
sidebar_order: 7
---

# The local secrets bundle

`~/.ichor/clusters/<name>/secrets.yaml` holds every CA key of the cluster. It is an age file
sealed with a passphrase, so a copy of it on a laptop backup or a cloud drive is
not a copy of the cluster's keys. `ichor init`, `ichor adopt --recover-secrets`
and `ichor export bundle` write it that way by default.

## The passphrase

The passphrase comes from, in order:

1. `--bundle-passphrase-file PATH`: the first line of the file. `PATH` may be a
   descriptor, so the passphrase need not touch the disk:
   `ichor restore ... --bundle-passphrase-file <(pass show ichor/demo)`.
2. The environment variable `ICHOR_BUNDLE_PASSPHRASE`. Anything running as you
   (and root) can read an environment, so prefer the file.
3. On a terminal: a prompt when opening a file; a generated passphrase, printed
   once, when creating one.

Never a flag value (it would be in `ps` and your shell history). A passphrase you
choose must be at least 12 characters. Every command that reads the bundle
(`restore cluster`, `adopt --secrets-bundle`, `create credentials
--bundle`, `export bundle`) takes it the same way. A wrong passphrase and
a damaged file give the same error, which names neither.

## Encrypting a bundle made before this change

A bundle file you made before this change still works, with a warning on every
use. Encrypt it:

```sh
ichor secrets encrypt demo              # ~/.ichor/clusters/demo/secrets.yaml
ichor secrets encrypt --file ./secrets.yaml
```

The command seals the file, opens the sealed copy again, compares it with the
original, and only then replaces the file.

> [!WARNING]
> If the plaintext file was ever copied
> or exposed, encrypting it does not undo that: rotate the CAs
> (`ichor rotate ca`).

The `age` tool opens the file without Ichor:
`age -d secrets.yaml`.

## The kubeconfig next to it

The kubeconfig next to it stays plaintext, because `kubectl` reads it. It holds
the `system:masters` override certificate (see
[Users and login](../users-and-login.md)); once login works, move it offline, but
keep it. The bundle can also make a new one: `ichor create credentials --bundle
FILE --kube-server URL --talos-endpoints ADDRESS` writes a kubeconfig and
talosconfig with no kubeconfig on disk. The bundle holds no addresses, so note
the API server's address with it. The Secret in
the cluster that holds the bundle for the controller is not affected.
