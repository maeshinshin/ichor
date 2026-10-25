# 0019. `ichor init`: scope, the namespace decision, and the gaps it leaves open

Status: Accepted · Date: 2026-10-25 · Design section: Bootstrap (`ichor init`)

## Context

`docs/design/architecture.md`'s "Bootstrap" section gives `ichor init` six
steps and one example invocation:

```
ichor init --preset standard --cni cilium [--argocd --repo URL --path clusters/NAME] [--gitops-dir DIR]
```

> 1. Generate the secrets bundle locally; nag the user to back it up.
> 2. Settle the shape of the cluster: topology preset, CNI preset, endpoint
>    (a VIP is recommended even for one node, because changing the endpoint
>    later means re-issuing certificates), disk roles for the first node.
> 3. Apply the rendered config to the first control plane. Its
>    `cluster.inlineManifests` carry Ichor itself, the CNI (unless flannel),
>    and, with `--argocd`, Argo CD plus a root Application pointing at the
>    repo. The repo credentials go in as a Secret from the CLI, never into
>    Git.
> 4. Bootstrap etcd, fetch the kubeconfig, store the bundle as a Secret.
> 5. Create the CRs. With `--gitops-dir`, also write them as YAML and
>    generate the repository skeleton described below.
> 6. Hand over: once Argo CD reports each bootstrapped component Synced and
>    Healthy, remove it from `inlineManifests`.

This phase builds steps 1-5. Step 6 is excluded on purpose: it is a
continuous reconciliation loop against Argo CD's own status (watch, poll,
retry), not a one-shot action a CLI invocation can perform and then exit.
It belongs to the controller, as a later unit's work.

## Decision: the two settled questions

**VIP is recommended, not required.** `buildInitPlan` accepts any of the
CRD's four endpoint types (`vip`, `loadBalancer`, `dns`, `node`) and only
prints a warning -- never an error -- when the type isn't `vip`, carrying
the design document's own reason in the warning text (changing the
endpoint later means re-issuing every certificate in the cluster). The
design's wording ("recommended") is deliberate and distinguishable from
the CRD's other hard requirements (e.g. control-plane count), so init does
not upgrade a recommendation into a rejection on its own authority.

**Insecure apply assumes a separate provisioning network, and says so.**
`ichor init --help`'s step 3 and the interactive confirmation prompt both
state this explicitly, because `maintenanceApplier.ApplyInsecure` genuinely
cannot authenticate the node it's talking to -- that is what "insecure"
means here, per architecture.md's "Trust" section -- so the only thing
this command can do about the risk is make sure the operator sees the
assumption stated plainly before confirming, not enforce a network
topology it has no way to check.

## Decision: one `--namespace`, not two

`TalosClusterSpec.SecretsRef`'s doc comment (ADR 0011) says the bundle
Secret lives in the controller's own namespace, which need not be the
`TalosCluster`'s namespace -- RBAC cannot express "the Secret this
`TalosCluster` names, wherever that `TalosCluster` lives" below cluster
scope. `ichor init` takes a single `-n/--namespace` flag and uses it for
both the `TalosCluster`/`TalosMachine`/`TalosConfigPatch` objects *and*
the bundle Secret.

This is a deliberate simplification, not a misunderstanding of ADR 0011:
architecture.md's "Deployment" section says the default is self-managed,
one controller inside the cluster it manages, in one namespace. For every
cluster `ichor init` creates, "the controller's namespace" and "the
namespace this command was told to use" are the same namespace by
construction -- there is exactly one namespace in play at bootstrap time,
because the controller that will eventually read this Secret hasn't been
deployed into some *other* namespace by this command. Hub mode (one Ichor
managing clusters from outside, "left open by the API but not built
first") would need a real distinction between "where this `TalosCluster`
lives" and "where its controller's Secret lives"; `ichor init` does not
attempt to build for that case now.

## Decision: three deliberately incomplete pieces, each flagged rather than faked

Ponytail's rule against inventing unrequested abstractions cuts the other
way here too: rather than build a plausible-looking but speculative
implementation of something no existing package supports, each of the
following is left undone and reported, the same way disk roles were
scoped out of this phase by the brief that assigned it.

**`cluster.inlineManifests` does not carry "Ichor itself" or "Argo CD
itself."** The design's step 3 names three things to inline: Ichor, the
CNI, and (with `--argocd`) Argo CD plus a root Application. Only the CNI
has a package behind it (`pkg/cni.Preset.BootstrapManifest`) that this
command can call without guessing. Neither Ichor nor Argo CD has a
published, version-pinned install manifest anywhere in this repository or
dependency set today: Ichor's own chart (`dist/chart`) is a `make
build-installer`-generated, uncommitted artifact with no story yet for how
a release binary would embed or fetch it, and Argo CD's own install
manifest would have to come from either a hardcoded upstream URL (a
version-pinning decision with real security weight -- fetching and
applying arbitrary YAML with cluster-admin during bootstrap) or a vendored
copy (a packaging decision belonging with the rest of Ichor's release
tooling). Both are genuine design decisions for whoever builds Ichor's own
and Argo CD's own bootstrap distribution, not something `internal/cli`
should decide unilaterally while finishing this phase. Without them,
running with `--argocd` produces a GitOps skeleton and a repository
credentials Secret that are ready for use *once* Argo CD exists on the
cluster by some other means (installed by hand, or by a future unit) --
not a cluster where Argo CD is already running.

**The CNI's Argo CD Application is not written into the GitOps
skeleton's `apps/` directory.** `pkg/cni.Preset.ArgoCDApplication` returns
`*unstructured.Unstructured`; `pkg/gitops.Skeleton.Apps` takes
`map[string]*gitops.Application`, a different, independently-owned typed
representation designed (ADR 0015) specifically to avoid depending on
Argo CD's own Go module. There is no conversion between the two today.
Writing one here, in `internal/cli`, would mean either reimplementing
`unstructured`-to-`gitops.Application` field mapping (knowledge that
belongs with one of those two packages, not with the command that merely
calls them) or weakening `pkg/gitops`'s typed `Apps` field to accept
`any`, which is `pkg/gitops`'s decision to make, not `ichor init`'s.
`buildSkeleton`'s doc comment names this gap at the point it would
otherwise silently drop the CNI's Application.

**Disk roles for the first node are not set.** Named in the design's step
2, but `TalosMachineSpec` carries no field for them (the volume allow-list
another unit is building does not exist yet), and `pkg/render`'s base
config does not set `machine.install` either -- its client-validation mode
explicitly sets `RequiresInstall` to `false`. There is nothing on either
side of the render for `ichor init` to populate. The node this command
targets has to already know its own install disk, e.g. a
`talos.install.disk=` kernel argument on the ISO or Image Factory image it
booted from.

## Decision: only `--preset standard`

The design names a `--preset` flag but the roadmap defers other presets to
later phases. `buildInitPlan` rejects anything but `"standard"` outright,
with a message naming exactly that ("not supported yet; only \"standard\"
is"), rather than silently treating an unrecognized preset as standard or
ignoring the flag.

## Consequences

- `ichor init --argocd --gitops-dir DIR` without Argo CD already installed
  on the target cluster produces a correct, ready-to-use GitOps skeleton
  and (if `--repo-username`/`--repo-password` were given) a repository
  credentials Secret, but does not itself bring Argo CD up. An operator
  following this path installs Argo CD by some other means before its
  root Application can do anything.
- The three gaps above are a natural shape for follow-up units:
  "Ichor-itself and Argo-CD-itself bootstrap manifest sourcing" (one
  decision, affecting both), "a `pkg/gitops` or `pkg/cni` conversion for
  CNI Applications," and "disk roles on `TalosMachineSpec`" (already
  being built by another unit, per the brief that scoped this phase).
- Hub mode, if built later, will need `ichor init` (or whatever supersedes
  it) to take the controller's namespace and this `TalosCluster`'s
  namespace as two separate flags; today's single `--namespace` is
  correct only because the self-managed default makes them the same
  namespace.
