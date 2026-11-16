---
sidebar_order: 4
---

# Create users and roles

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
| `admin` | operator, plus delete, user administration, sign-in settings, the Cilium Helm values and the destructive operations |

A role is a group in Kubernetes. A custom permission is a ClusterRole bound to
the group `ichor:<name>` of the user. No built-in role can read secrets.

Changing the Cilium Helm values (`spec.cni.cilium.values`: the Cilium page, `ichor
apply cni-values` or `ichor edit cni-values`, `ichor adopt --cilium-values`, `kubectl`) needs the verb
`update` on `talosclusters/cilium-values`, which only `admin` holds:
the chart can run privileged pods on every node. An `operator` sees the values
read-only, and a write of theirs that changes the values is refused; an edit of the
cluster that leaves them as they are is not. To let someone else change them, bind
a ClusterRole with that verb to their group.

A new user has no password until an admin sets one. The admin can set a
password, or ask for a generated one, which is shown once. The user changes it
at the next login. A password is 12 to 128 bytes long, with no other rules.

Users can change their own password in the account menu.

Two rules keep the roles from being worn away:

- A user may grant only roles they hold (admin: any); Kubernetes RBAC alone
  used to decide this. A validating webhook on `ichorusers` applies the same rule
  to a `kubectl` write made as an Ichor user; an external identity
  stays gated by RBAC alone.
- The last enabled admin cannot be deleted, disabled or demoted, in the UI, the CLI
  or with `kubectl`. The `system:masters` override certificate below
  is exempt, and so are the controller and the Kubernetes namespace deletion, so
  deleting the `ichor-system` namespace still works.

## The `ichor user` commands


They talk to ichor-ui directly at the issuer URL over https, with your login (a
token for ichor-ui, from the browser sign-in; the first command opens it). They
do not use a port-forward and need no kubeconfig, so they work from a laptop.
Give the URL with `--issuer` or `ICHOR_ISSUER`, and `--ca-file` (or
`ICHOR_CA_FILE`) when a private CA signed ichor-ui's certificate. The commands
print for people; `-o yaml` or `-o json` print the full response for scripts.

```sh
ichor get users
ichor describe user alice
ichor create user alice --display-name "Alice" --role viewer --generate
ichor edit user alice --role operator        # only the flags you pass change
ichor edit user alice --disabled=true
ichor user set-password alice --password-stdin
ichor user revoke-sessions alice
ichor delete user alice                        # asks; --yes skips
```

`list` is a table, and a write prints one line naming what it did:

```console
$ ichor get users
NAME   DISPLAY NAME  EMAIL  ROLES   STATE        AGE
admin  -             -      admin   active       2d
alice  Alice         -      viewer  no password  4s

$ ichor create user bob --role viewer --generate
✓ created user bob (viewer)
! Password for bob (shown once, stored nowhere; they must change it at the first sign-in): Zq3x...

$ ichor edit user bob --role operator
✓ updated user bob
```

The state is `active`, `no password` (nobody can sign in as the user yet) or
`disabled`.

`create` and `set-password` take the password from a prompt with no echo (asked
twice), from the first line of stdin with `--password-stdin`, or generate one
with `--generate`; a generated password is printed once to stderr, under the
result line, and nowhere else. A password is never a flag value. The password set by an admin is marked must-change.
Kubernetes RBAC decides who may do what: the admin role for everything,
`update` on `ichorusers/password` for `set-password` and `revoke-sessions`.
