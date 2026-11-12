# 0156. Files derived from Talos are MPL-2.0, and NOTICE names the ones that cannot say so

Status: Accepted · Date: 2026-11-13 · Design section: Trust · Touches: 0003, 0150

Decided: a Go file that contains Talos-derived content carries the MPL-2.0 header and no Apache block; a generated manifest or reference page that holds the same content cannot carry a header and is named in `NOTICE`, which also says where the covered source is obtained.

## Context

Ichor is Apache-2.0. The Talos machinery module is MPL-2.0 with a per-file notice. Ichor generates Go types, a CRD union and CRD schemas from it, so some of its own files hold Talos's kind strings, type names, field names and documentation text. Such a file is a Modification under MPL-2.0 section 1.10(b), and the licence applies to that file; the Larger Work stays under Apache-2.0 (section 3.3). Releases distribute Executable Form, so section 3.2 obliges the distributor to say where the source of the covered files is available. `NOTICE` named three Go files as the only covered files and gave no such pointer.

## Decision

1. **One licence per file.** A Go file with Talos-derived content carries the MPL-2.0 Exhibit A header and `// Copyright 2026 maeshinshin.`, and no Apache block. The union file `api/v1alpha1/zz_generated.talosconfigpatchdocument.go` is one of them, with the same header as the typedconfig files, emitted by the generator that writes it.
2. **A generated manifest takes no header.** The Talos CRDs (`TalosCluster`, `TalosConfigPatch`), their copies in the Helm chart, the install manifest that embeds them and the reference pages that publish them hold the same content, but a header in generated YAML is overwritten by generation and is not part of a Kubernetes manifest. `NOTICE` names them as schema derived from Talos machinery and covered by MPL-2.0.
3. **`NOTICE` is the source offer.** It travels in every release archive, every image and the Helm chart. It states that the MPL-2.0 covered files are available in source form at the Ichor repository, at the Git tag matching the artifact's version. A new covered file is added to `NOTICE` in the change that creates it.
4. **Embedded data is listed with the third-party components.** The Sigstore public-good trusted root embedded in the controller is Apache-2.0 data from the Sigstore root-signing repository and appears in `THIRD-PARTY-NOTICES` beside the embedded Cilium chart. The older Cilium chart kept under `pkg/cni/cilium/chart` is used by tests and is neither embedded nor shipped, so it is not listed.
5. **Copyleft is refused.** `THIRD-PARTY-NOTICES` is not written when a dependency's licence text is GPL, LGPL, AGPL, SSPL or BUSL; the error names the dependency and the licence. MPL-2.0 is allowed, per file, as it is today. Taking a copyleft dependency needs an ADR first.

## Consequences

A recipient of a binary, an image or the chart can find the covered source. The set of covered files is explicit, and a contributor adds a Talos-derived file by adding it to `NOTICE` or by generating it with the header. The CRD YAML itself stays unlabelled; that is a known limit of generated manifests, and `NOTICE` is where it is recorded.
