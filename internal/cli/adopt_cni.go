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
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CNI detection and the refusal rules, section 1.1. Provenance and kind are decided
// from the Kubernetes API (the DaemonSets that are running), with the Talos-side selection
// (cluster.network.cni) as a hint where the API shows nothing.

var crdGVK = schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}

// cniKind is the CNI found; the values are the ones status.cni.detected will use.
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
		case s == string(detectedCilium) || strings.Contains(s, "cilium/cilium") || strings.Contains(s, "/cilium:"):
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
	// NoCNI: the cluster has no CNI at all: Talos says none, no CNI workload or
	// CiliumNode CRD exists, and every node is NotReady with the network-plugin message.
	NoCNI bool
	// NodesWithoutPodCIDR names the nodes with no spec.podCIDR (a precondition of 1.3).
	NodesWithoutPodCIDR []string
}

// Mixed reports whether more than one CNI is installed.
func (r *cniReport) Mixed() bool { return len(r.Kinds) > 1 }

// String is the one-line description used in plans and refusals.
func (r *cniReport) String() string {
	if r.NoCNI {
		return "none (no CNI installed, every node NotReady)"
	}

	if r.Kind == detectedCilium && r.Provenance != "" {
		return fmt.Sprintf("cilium (%s)", r.Provenance)
	}

	return string(r.Kind)
}

// detectCNI builds the report from the Kubernetes facts and the Talos-side CNI selection.
func detectCNI(f *k8sFacts, talos CNIFacts) *cniReport {
	rep := &cniReport{
		TalosCNINone: talos.Kind == CNINone,
		CustomURLs:   talos.Kind == CNICustom && len(talos.URLs) > 0,
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
		// Nothing is running. A node that Talos would give flannel is that; anything else has no CNI.
		rep.Version = ""

		if talos.Kind == CNIFlannel {
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

	rep.NoCNI = rep.Kind == detectedNone && rep.TalosCNINone && !f.CiliumNodeCRD && len(f.Nodes) > 0

	for _, n := range f.Nodes {
		rep.NoCNI = rep.NoCNI && !n.Ready && n.NetworkNotReady

		if n.PodCIDR == "" {
			rep.NodesWithoutPodCIDR = append(rep.NodesWithoutPodCIDR, n.Name)
		}
	}

	if rep.Kind == detectedCilium && !rep.Mixed() {
		rep.Provenance = ciliumProvenance(f)
	}

	return rep
}

func ciliumProvenance(f *k8sFacts) cniProvenance {
	foreign := false

	for _, ns := range f.CiliumReleaseNamespaces {
		if ns == ichorControllerNamespace {
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

// checkCNIAdoption applies the refusal rules, 1.1: a mixed cluster is refused even
// with --cni-unmanaged, and a non-Cilium cluster is refused unless unmanaged was requested. A nil
// error means the adoption may proceed in the requested mode.
func checkCNIAdoption(r *cniReport, unmanaged bool) error {
	if r.Mixed() {
		return fmt.Errorf("the cluster runs more than one CNI (%s); Ichor cannot say what it would be leaving alone, "+
			"so it adopts neither mode. Remove one of them and run adopt again", strings.Join(r.Found, ", "))
	}

	if r.NoCNI {
		if unmanaged {
			return errors.New("this cluster has no CNI, so no pod can run on it, including Ichor's. " +
				"Run again without --cni-unmanaged and Ichor installs Cilium, or install a CNI yourself first")
		}

		return nil
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
		"to adopt the cluster with the CNI left exactly as it is, run again with --cni-unmanaged. "+
		"Ichor does not migrate a CNI: to have it manage Cilium instead, the cluster needs Cilium with ipam.mode kubernetes, or no CNI; "+
		"see \"Moving from flannel\" in docs/guides/adopt-and-reclaim.md", what)
}

const (
	kubePrismDoc         = "the KubePrismConfig document"
	kubePrismDocContract = "v1.14"
)

// kubePrismSetting names where KubePrism is switched on for a version contract: the KubePrismConfig
// document from v1.14, machine.features.kubePrism before. An empty contract is the newest.
func kubePrismSetting(contract string) string {
	if contract == "" || semver.Compare(semver.MajorMinor(contract), kubePrismDocContract) >= 0 {
		return kubePrismDoc
	}

	return "machine.features.kubePrism"
}

// checkNoCNIPreconditions checks KubePrism on 7445 on every node (checked against the
// Talos facts of nodes, which the caller passes as name -> setting) and a pod CIDR on every Node.
func checkNoCNIPreconditions(r *cniReport, kubePrism map[string]KubePrismFacts, contract string) error {
	var bad []string

	for name, kp := range kubePrism {
		if !kp.Enabled || (kp.Port != 0 && kp.Port != 7445) {
			bad = append(bad, name)
		}
	}

	if len(bad) > 0 {
		slices.Sort(bad)

		return fmt.Errorf("this cluster has no CNI, so Ichor must install Cilium, which needs KubePrism on port 7445 on every node in both kube-proxy modes "+
			"(with kube-proxy replaced, Cilium also reaches the API through it), but KubePrism is not enabled there on %s. "+
			"Enable KubePrism (%s, port 7445) with your usual tool, then run adopt again",
			strings.Join(bad, ", "), kubePrismSetting(contract))
	}

	if len(r.NodesWithoutPodCIDR) > 0 {
		return fmt.Errorf("this cluster has no CNI, so Ichor must install Cilium with ipam.mode=kubernetes, "+
			"which needs a spec.podCIDR on every node; missing on %s. Enable node CIDR allocation in kube-controller-manager, then run adopt again",
			strings.Join(r.NodesWithoutPodCIDR, ", "))
	}

	return nil
}
