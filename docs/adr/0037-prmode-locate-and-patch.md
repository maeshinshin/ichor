# 0037. PR mode's file-finding and byte-preserving YAML edit, as `pkg/prmode`

Status: Superseded by ADR 0101 · Date: 2026-10-26 · Design section: GitOps and Argo CD (phase 8, "PR mode")

Decided: built `pkg/prmode`'s `ClassifySource`/`Locate`/`Patch`/`Diff` -- finding a tracked CR's file via its Application's source and editing one field with go-yaml's `yaml.Node`, never a PR host API -- as its own host-independent, fully unit-testable unit, separate from opening a PR

## Context

architecture.md's phase-8 "PR mode" paragraph does three things in one
breath: find the file, edit it preserving formatting, and open a PR whose
description carries an impact preview. The first two are pure, local,
host-independent operations with no external dependency once a checked-out
repository exists; the third needs a GitHub/GitLab/Gitea-Forgejo API call, a
network connection, and an impact-preview backend (`PreviewImpact`, ADR 0035). Mixing them would drag the exhaustively-testable half
(formatting preservation under every YAML shape a real manifest uses) into
the half that cannot be tested without a live host. `pkg/prmode` is only the
first two.

## Decision: `pkg/prmode`, not `internal/cli`

Both the CLI and the UI need this (architecture.md: "the UI and CLI don't
write it"), so it is a `pkg/` package -- the same reasoning `pkg/gitops` and
`pkg/upgrade` already follow -- not something built inside `internal/cli`
that the UI backend could not import.

## Decision: `yaml.Node` round-tripping, not unmarshal-to-struct-then-marshal

A plain
unmarshal into a Go struct and marshal back loses comments outright,
reorders keys to the struct's field order, and normalizes quote style.
`go.yaml.in/yaml/v4` (already this project's own YAML dependency --
`internal/cli/bundlefile.go`, `pkg/gitops` -- a fork-successor of
`gopkg.in/yaml.v3` with the same `yaml.Node` API) decodes a document into a
node tree whose comments, key order, anchors/aliases, quote style and
block/flow list style survive a decode-mutate-encode round trip, **provided
the encoder's indent width is set to match the document's own**:
`yaml.NewEncoder(...)` without `SetIndent(n)`
uses a default width that does not match every input and silently widens a
2-space document. `detectIndent` (`pkg/prmode/patch.go`) measures the
document's own first indented line rather than assuming one width.

One limitation is not worked around: a
blank line with no comment attached to it, between two sibling keys *inside
the document actually being edited*, is dropped by go-yaml's own Node
round trip (blank lines are only preserved when attached to a comment).
`TestPatch_BlankLineBetweenKeysIsDropped` (`pkg/prmode/patch_test.go`)
records this as a known, intentional fact rather than leaving it to be
rediscovered as a bug later. It does not affect any document Patch does not
touch, nor any comment-attached blank line, nor any blank line in a
document it does touch that is not between two keys both re-encoded.

## Decision: only the matched document is ever re-rendered

A file can hold more than one `---`-separated document. `splitDocuments`
(`pkg/prmode/yamldoc.go`) partitions a file into raw byte segments on a
column-0 `---` line -- safe without a full YAML lexer, because block scalar
content is always indented past the key that introduces it, so a column-0
`---` can never be anything but a real document-stream marker. (shortcut:
this does not hold inside an open flow collection spanning multiple lines,
a shape real Kubernetes manifests do not use.) `Patch` re-encodes only the
one segment whose `apiVersion`/`kind`/`metadata.name`/`metadata.namespace`
match `Target`; every other segment's bytes, and every separator line, are
copied into the result unchanged -- not reused by convention, but never
decoded at all. This is also what makes "only the right document changes
when a file holds more than one CR" true by construction, not by a careful
encoder setting that might regress.

## Decision: editing is single-field, mapping-keys-only

`FieldUpdate.Path` is a sequence of mapping keys; `setNode` has no list-index
support. architecture.md's "edit the right node" never asks for list-indexed
paths, and every field PR mode might plausibly edit today
(`TalosCluster.spec.*` and its siblings) is nested maps. Adding index support
speculatively is left as a documented shortcut in `setNode`'s own comment,
not built.

## Decision: Helm/Jsonnet classification reads raw unstructured fields, not `pkg/gitops.ApplicationSource` alone

`pkg/gitops.ApplicationSource` (built for the *write* side, Ichor's own
skeleton) has no `plugin` field at all, because Ichor's own Applications
never use a Config Management Plugin. Decoding an externally-defined,
possibly-Jsonnet-via-CMP Application straight into that struct would make a
plugin source indistinguishable from a plain path source -- both would show
a non-empty `Path` and an empty `Chart`. `ClassifySource`
(`pkg/prmode/source.go`) checks for `spec.source.plugin`'s presence directly
on the unstructured object before ever decoding into `ApplicationSource`,
so a plugin source is caught as `SourcePlugin` (diff-only) rather than
misclassified as editable. `spec.sources` (plural, multi-source
Applications) is also read directly and treated as unsupported outright:
architecture.md's "find the file via the Application's source" describes
one source, and guessing which of several holds a given CR risks editing
the wrong repository.

## Decision: `Diff` is a two-string unified diff, nothing more

For `SourceHelm`/`SourcePlugin`, architecture.md asks for "a diff to apply
by hand" -- but there is no single file to diff against for a templated or
plugin-rendered CR, only its current and about-to-be-applied YAML
renderings, which the caller already has (from the cluster, not from a
file this package would have to locate). `Diff` (`pkg/prmode/diff.go`) is
therefore a thin wrapper over `github.com/pmezard/go-difflib` (already an
indirect dependency; promoted to direct here rather than hand-rolling a
diff algorithm) taking two YAML strings and a display name, with no
filesystem or source-resolution logic of its own.

## Consequences

- Not built here: opening a PR against any host (GitHub, GitLab,
  Gitea/Forgejo), which is a separate interface (ADR 0038). The UI following a PR through merge/sync/apply, the impact
  preview text in a PR's description, strict mode, and override are
  likewise untouched.
- `Locate` takes an already-checked-out directory; cloning or fetching a
  repository from `ApplicationSource.RepoURL`/`TargetRevision` belongs to
  the host-specific layer, not this package.
- `internal/cli/gitops_tracking.go`'s existing `isTracked` (Argo CD via
  `argocd-cm`, Flux via the `flux-client-side-apply` managed-fields
  signal) is reused as-is for "is this CR tracked"; there is no
  second tracking check. Resolving *which* Application tracks a CR (so its
  `spec.source` can be read) for the Flux case is not built: Flux has no
  single Application-shaped object with a `.spec.source` the way Argo CD
  does (ownership lives in a Kustomization's `.status.inventory`, pointing
  at a separate `GitRepository`), and architecture.md's own "find the file
  via the Application's source" wording is Argo CD vocabulary. `ClassifySource`
  takes an Argo CD Application directly; wiring "CR -> owning Application"
  (via the tracking annotation/label's own value) and the Flux equivalent
  are both left to the caller.
