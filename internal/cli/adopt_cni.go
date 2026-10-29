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

package cli

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CNI detection and the refusal rules of ADR 0103, section 1.1. Provenance and kind are decided
// from the Kubernetes API (the DaemonSets that are running), with the Talos-side selection
// (cluster.network.cni) as a hint where the API shows nothing.

var crdGVK = schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}

func isNoMatch(err error) bool { return meta.IsNoMatchError(err) }

// cniKind is the CNI found; the values are the ones status.cni.detected will use (ADR 0103, 1.1a).
type cniKind string

const (
	detectedCilium  cniKind = "cilium"
	detectedFlannel cniKind = "flannel"
	detectedCalico  cniKind = "calico"
	detectedOther   cniKind = "other"
	detectedNone    cniKind = "none"
)

// cniProvenance says how a Cilium got onto the cluster (Cilium only).
type cniProvenance string

const (
	// provenanceIchor: Ichor's own Helm release (record in ichor-system). Nothing to take over.
	provenanceIchor cniProvenance = "ichor"
	// provenanceHelm: a Helm release record exists outside ichor-system (helm, cilium-cli).
	provenanceHelm cniProvenance = "helm"
	// provenanceManifest: no release record; a Talos inline manifest or an applied `helm template`.
	provenanceManifest cniProvenance = "manifest"
)

// classifyDaemonSet returns the CNI a DaemonSet is the agent of, or "" if it is not a CNI agent.
// Names decide first; images catch renamed ones.
func classifyDaemonSet(name string, containers []corev1.Container) cniKind {
	candidates := make([]string, 0, 1+len(containers))
	candidates = append(candidates, strings.ToLower(name))

	for _, c := range containers {
		candidates = append(candidates, strings.ToLower(c.Image))
	}

	for _, s := range candidates {
		switch {
		case s == "cilium" || strings.Contains(s, "cilium/cilium") || strings.Contains(s, "/cilium:"):
			return detectedCilium
		case strings.Contains(s, "flannel"):
			return detectedFlannel
		case strings.Contains(s, "calico") || s == "canal":
			return detectedCalico
		case strings.Contains(s, "weave"), strings.Contains(s, "antrea"), strings.Contains(s, "kube-router"),
			strings.Contains(s, "kube-ovn"), strings.Contains(s, "multus"):
			return detectedOther
		}
	}

	return ""
}

// talosCNIHint is the Talos-side CNI selection, filled by the Talos facts (unit 1). The zero
// value means "not read".
type talosCNIHint struct {
	// Name is cluster.network.cni.name: "", "flannel", "none", "custom" or "cilium".
	Name string
	// URLs are cluster.network.cni.urls; non-empty means Talos applies those manifests.
	URLs []string
}

// cniReport is the result of the read-only CNI inspection.
type cniReport struct {
	Kind       cniKind
	Provenance cniProvenance // Cilium only
	// Version is the image tag of the CNI's DaemonSet where there is one.
	Version string
	// Found lists every CNI agent DaemonSet as "namespace/name (kind)".
	Found []string
	// Kinds is the distinct set of CNI kinds found; more than one means mixed.
	Kinds []cniKind
	// TalosCNINone: Talos is told to install no CNI (cluster.network.cni.name: none).
	TalosCNINone bool
	// CustomURLs: cni.name is custom with manifest URLs, an unrepresentable config path (class C).
	CustomURLs bool
}

// Mixed reports whether more than one CNI is installed.
func (r *cniReport) Mixed() bool { return len(r.Kinds) > 1 }

// String is the one-line description used in plans and refusals.
func (r *cniReport) String() string {
	if r.Kind == detectedCilium && r.Provenance != "" {
		return fmt.Sprintf("cilium (%s)", r.Provenance)
	}

	return string(r.Kind)
}

// detectCNI builds the report from the Kubernetes facts and the Talos hint.
func detectCNI(f *k8sFacts, hint talosCNIHint) *cniReport {
	rep := &cniReport{
		TalosCNINone: hint.Name == "none",
		CustomURLs:   hint.Name == "custom" && len(hint.URLs) > 0,
	}

	seen := map[cniKind]bool{}

	for _, ds := range f.CNIDaemonSets {
		rep.Found = append(rep.Found, fmt.Sprintf("%s/%s (%s)", ds.Namespace, ds.Name, ds.Kind))

		if !seen[ds.Kind] {
			seen[ds.Kind] = true

			rep.Kinds = append(rep.Kinds, ds.Kind)
		}

		if rep.Version == "" {
			rep.Version = imageTag(ds.Image)
		}
	}

	switch {
	case len(rep.Kinds) == 0:
		// Nothing is running. A node that Talos would give flannel (name unset or flannel) is that;
		// anything else has no CNI.
		rep.Version = ""

		if hint.Name == "" || hint.Name == "flannel" {
			rep.Kind = detectedFlannel
		} else {
			rep.Kind = detectedNone
		}
	case len(rep.Kinds) == 1:
		rep.Kind = rep.Kinds[0]
	default:
		// Mixed: Kind is the first found; callers must check Mixed first.
		rep.Kind = rep.Kinds[0]
	}

	if rep.Kind == detectedCilium && !rep.Mixed() {
		rep.Provenance = ciliumProvenance(f)
	}

	return rep
}

func ciliumProvenance(f *k8sFacts) cniProvenance {
	foreign := false

	for _, r := range f.CiliumReleases {
		if r.Namespace == ichorControllerNamespace {
			return provenanceIchor
		}

		foreign = true
	}

	if foreign {
		return provenanceHelm
	}

	return provenanceManifest
}

func imageTag(image string) string {
	image = strings.SplitN(image, "@", 2)[0]
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		return image[i+1:]
	}

	return ""
}

// checkCNIAdoption applies the refusal rules of ADR 0103, 1.1: a mixed cluster is refused even
// with --cni-unmanaged, and a non-Cilium cluster is refused unless unmanaged was requested. A nil
// error means the adoption may proceed in the requested mode.
func checkCNIAdoption(r *cniReport, unmanaged bool) error {
	if r.Mixed() {
		return fmt.Errorf("the cluster runs more than one CNI (%s); Ichor cannot say what it would be leaving alone, "+
			"so it adopts neither mode. Remove one of them and run adopt again", strings.Join(r.Found, ", "))
	}

	if r.Kind == detectedCilium || unmanaged {
		return nil
	}

	what := string(r.Kind)

	switch r.Kind {
	case detectedNone:
		what = "no CNI (cluster.network.cni.name is none)"
	case detectedFlannel:
		what = "flannel"
	}

	return fmt.Errorf("the cluster runs %s. Ichor will not install or operate it; "+
		"to adopt the cluster with the CNI left exactly as it is, run again with --cni-unmanaged", what)
}
