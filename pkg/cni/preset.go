/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package cni defines what a CNI preset is, so that adding one is nothing
// more than a new implementation of Preset (docs/design/architecture.md,
// "CNI"). Ichor bootstraps a CNI once and hands it over to Argo CD; it never
// installs, upgrades or otherwise operates one afterwards, which is why the
// interface stops at rendering, a consistency check and a hand-over signal,
// and has no method that reaches into a running cluster on its own.
package cni

import (
	"context"
	"fmt"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitops"
)

// InlineManifestName is the name `ichor init` writes the CNI's bootstrap
// manifest under in cluster.inlineManifests (internal/cli's own cniKey,
// unexported there and duplicated here because that package and this
// one's callers -- pkg/render, internal/controller -- have no shared home
// below internal/cli that isn't a layering violation). The hand-over
// reconciler (ADR 0023) matches on this exact name to know which
// inlineManifests entry to stop rendering once Argo CD has adopted it.
const InlineManifestName = "cni"

// KubeProxyMode is TalosCluster.spec.cni.kubeProxy: kube-proxy stays Talos's
// own, or the CNI replaces it.
type KubeProxyMode string

const (
	// KubeProxyEnabled is Talos's own default: kube-proxy runs.
	KubeProxyEnabled KubeProxyMode = "enabled"

	// KubeProxyReplaced means the CNI takes over kube-proxy's job. The
	// setting then has two owners -- Talos (cluster.proxy.disabled, written
	// by Ichor) and the CNI (written by Argo CD) -- which is exactly what
	// Preset.CheckConsistency exists to keep honest.
	KubeProxyReplaced KubeProxyMode = "replaced"
)

// Validate reports whether m is one of the two values the CRD's enum
// allows. Callers that only ever see values the API server already
// validated don't need this; it exists for Preset implementations called
// directly, e.g. from a unit test.
func (m KubeProxyMode) Validate() error {
	switch m {
	case KubeProxyEnabled, KubeProxyReplaced:
		return nil
	default:
		return fmt.Errorf("cni: kubeProxy %q is invalid: must be %q or %q", m, KubeProxyEnabled, KubeProxyReplaced)
	}
}

// LiveConfig is what Preset.CheckConsistency compares against: the CNI's
// own running configuration, as read by the caller (the future reconciler;
// reading it is not this package's job -- see CheckConsistency).
type LiveConfig struct {
	// KubeProxyReplacement is the CNI's own live notion of whether it has
	// taken over kube-proxy's job, e.g. read from Cilium's cilium-config
	// ConfigMap.
	KubeProxyReplacement bool
}

// WorkloadRef names a workload whose readiness is part of a successful
// hand-over signal.
type WorkloadRef struct {
	// Namespace is the workload's namespace.
	Namespace string

	// Kind is the workload's kind, e.g. "DaemonSet" or "Deployment".
	Kind string

	// Name is the workload's name.
	Name string
}

// Preset is a CNI: Talos-side patches, manifest rendering for the
// bootstrap, an Argo CD Application template, whether it can replace
// kube-proxy, a consistency check against the live configuration, and the
// workloads that signal a successful hand-over
// (docs/design/architecture.md, "CNI"). A new CNI is a new implementation
// of this interface and nothing else -- it carries no other extension
// point.
type Preset interface {
	// TalosPatches returns the Talos-side config documents this preset
	// needs for kubeProxy, meant to be applied as a cluster-layer
	// TalosConfigPatch. It returns an error if kubeProxy is "replaced" and
	// SupportsKubeProxyReplacement is false.
	TalosPatches(kubeProxy KubeProxyMode) ([]ichorv1alpha1.TalosConfigPatchDocument, error)

	// BootstrapManifest renders what Ichor applies once, before Argo CD
	// exists, to bring the CNI up. A preset that needs nothing beyond
	// Talos's own default (flannel) returns nil.
	BootstrapManifest(kubeProxy KubeProxyMode) ([]byte, error)

	// ArgoCDApplication renders the Argo CD Application that takes over
	// from the bootstrap manifest. A preset Ichor never hands to Argo CD
	// (flannel) returns nil. Returning pkg/gitops's own typed Application,
	// rather than *unstructured.Unstructured, means every Application this
	// package builds goes through pkg/gitops's guards (RepoURL credential
	// checks, the ServerSideApply/ignoreDifferences pitfalls, Secret
	// write refusal) instead of a caller having to re-check an
	// unstructured value by hand (ADR 0013's update).
	ArgoCDApplication(kubeProxy KubeProxyMode) (*gitops.Application, error)

	// SupportsKubeProxyReplacement reports whether this preset can run
	// with kube-proxy replaced.
	SupportsKubeProxyReplacement() bool

	// CheckConsistency compares live against what kubeProxy declares and
	// returns a non-nil error describing any mismatch -- reported by the
	// caller as a health failure. It makes no cluster calls itself: live is
	// already read.
	CheckConsistency(ctx context.Context, live LiveConfig, kubeProxy KubeProxyMode) error

	// HandoverWorkloads returns the workloads whose readiness signals a
	// successful hand-over to Argo CD. A preset Ichor never hands over
	// (flannel) returns nil.
	HandoverWorkloads() []WorkloadRef
}
