# 0134. `ichor init --merge-kubeconfig` merges the new cluster into the user's kubeconfig

Status: Accepted · Date: 2026-11-06 · Design section: Bootstrap (`ichor init`) · Builds on: 0127

Decided: `ichor init --merge-kubeconfig` (opt-in) merges the cluster-admin kubeconfig into the kubeconfig kubectl would write to (the first existing file of `$KUBECONFIG`, else its first path, else `~/.kube/config`; `--kubeconfig` wins), as cluster `<name>`, user and context `admin@<name>`, renames on a collision instead of overwriting, and makes the new context current. The copy at `~/.ichor/clusters/<name>/kubeconfig` is written as before. Without the flag init touches nothing outside `~/.ichor` and prints the `export KUBECONFIG=` line.

## Context

init saves the kubeconfig under `~/.ichor/clusters/<name>/kubeconfig` and never touches `~/.kube/config`, so every later `ichor` or kubectl call needs `export KUBECONFIG=...`. `talosctl config merge` is the precedent for merging on request. A kubeconfig holds a client key, so the merge must not be silent.

## Decision

1. **Opt-in.** Rewriting a file the user keeps other clusters in is never a default.
2. **Target file.** The CLI's own resolution (`--kubeconfig`, `$KUBECONFIG`, `~/.kube/config`) with client-go v0.36.2's write target: `ClientConfigLoadingRules.GetDefaultFilename()` returns the explicit path, else the first existing file of `$KUBECONFIG`, else its first entry, else `~/.kube/config`. This is where `kubectl config set-context` would write. Only that one file is written; other files of a multi-path `$KUBECONFIG` are read (for name collisions) and left alone. The parent directory is created (0700).
3. **Names.** Cluster `<name>`, user and context `admin@<name>`. Talos writes the same names into the saved kubeconfig (`admin@<cluster>`), so the merged entry looks like the one `talosctl kubeconfig` makes.
4. **Collision.** If a cluster, user or context of that name exists (in any file of the loading chain) with different content, the cluster name gets the first free suffix (`demo-1`, `demo-2`, ...) and the user and context follow (`admin@demo-1`), as `talosctl config merge` renames. Nothing existing is overwritten or removed. If all three already exist with identical content (a repeated merge) nothing is rewritten. The final context name is printed.
5. **Current context.** Set to the merged context. The user asked for kubectl and ichor to talk to the new cluster; `ichor init` just built it, so it is what they want next, and it is said in the output. The alternative (leave it alone) would defeat the point of the flag, since `ichor` would still need `--context`.
6. **Write.** Load with `clientcmd.LoadFromFile`, edit the `api.Config`, `clientcmd.Write`, then write a temporary file in the same directory and rename it over the target (0600; `writeFileAtomic`). Credentials are never printed.
7. **Plan.** init's header says, before anything is applied, which context will be merged into which file, and the target is read and parsed then, so a malformed kubeconfig stops init before the irreversible apply.
8. **Next step.** init always ends with it: with the flag "kubectl and ichor now use context X"; without it the `export KUBECONFIG=` line and a hint about the flag.

## Alternatives rejected

- Merge by default: surprising writes to a shared file.
- Overwrite on a collision, or refuse: overwriting loses a user's entry; refusing after the node was applied leaves the user without a kubeconfig. Renaming is safe and reversible.
- Not setting current-context: kubectl keeps pointing at another cluster after the user asked for the merge.
- Writing to every file of `$KUBECONFIG`: kubectl itself writes new entries to one file.

## Risks

A later merge for a cluster with the same name after the old one was deleted adds `admin@demo-1`; the user deletes stale entries with `kubectl config delete-context`. A concurrent kubectl write between our read and rename can be lost (the rename is atomic, the read-modify-write is not locked, as in kubectl's own lock-file-less case).
