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
	"context"
	"fmt"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// The Kubernetes-side facts `ichor adopt` reads from the cluster it is adopting (ADR 0103, 1.1
// and 1.5). Everything here is read-only: Get and List, nothing else.

const (
	k8sFactsSystemNamespace = "kube-system"
	ciliumConfigMapName     = "cilium-config"
	ciliumKPRKey            = "kube-proxy-replacement"

	// helmReleaseSecretType and helmOwnerLabel identify Helm's release records
	// (sh.helm.release.v1.<name>.v<N>), which Helm stores as Secrets of this type.
	helmReleaseSecretType = corev1.SecretType("helm.sh/release.v1")
	helmOwnerLabel        = "owner"
	helmNameLabel         = "name"

	kprTrue = "true"
)

// daemonSetFact is a DaemonSet that looks like a CNI agent.
type daemonSetFact struct {
	Namespace string
	Name      string
	Image     string
	// Kind is the CNI the DaemonSet belongs to (detectedCilium, detectedFlannel, ...).
	Kind cniKind
}

// k8sFacts is what the Kubernetes API says about the CNI and kube-proxy.
type k8sFacts struct {
	// CNIDaemonSets are the DaemonSets recognised as a CNI agent, sorted by namespace and name.
	CNIDaemonSets []daemonSetFact

	// CiliumConfig is cilium-config's data; nil when the ConfigMap is absent.
	CiliumConfig map[string]string

	// CiliumReleaseNamespaces are the namespaces holding Helm release records named "cilium".
	CiliumReleaseNamespaces []string
}

// gatherK8sFacts reads the CNI and kube-proxy facts from r.
func gatherK8sFacts(ctx context.Context, r client.Reader) (*k8sFacts, error) {
	facts := &k8sFacts{}

	var dsl appsv1.DaemonSetList
	if err := r.List(ctx, &dsl); err != nil {
		return nil, fmt.Errorf("listing DaemonSets: %w", err)
	}

	for i := range dsl.Items {
		ds := &dsl.Items[i]

		image := ""
		if cs := ds.Spec.Template.Spec.Containers; len(cs) > 0 {
			image = cs[0].Image
		}

		if kind := classifyDaemonSet(ds.Name, ds.Spec.Template.Spec.Containers); kind != "" {
			facts.CNIDaemonSets = append(facts.CNIDaemonSets, daemonSetFact{Namespace: ds.Namespace, Name: ds.Name, Image: image, Kind: kind})
		}
	}

	slices.SortFunc(facts.CNIDaemonSets, func(a, b daemonSetFact) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})

	var cm corev1.ConfigMap

	switch err := r.Get(ctx, types.NamespacedName{Namespace: k8sFactsSystemNamespace, Name: ciliumConfigMapName}, &cm); {
	case err == nil:
		facts.CiliumConfig = cm.Data
		if facts.CiliumConfig == nil {
			facts.CiliumConfig = map[string]string{}
		}
	case !apierrors.IsNotFound(err):
		return nil, fmt.Errorf("reading ConfigMap %s/%s: %w", k8sFactsSystemNamespace, ciliumConfigMapName, err)
	}

	releases, err := ciliumHelmReleases(ctx, r)
	if err != nil {
		return nil, err
	}

	facts.CiliumReleaseNamespaces = releases

	return facts, nil
}

// ciliumHelmReleases lists Helm's release records for the release named "cilium", in every
// namespace. Only labels are read; the record payload is not decoded here (unit 5 does that).
func ciliumHelmReleases(ctx context.Context, r client.Reader) ([]string, error) {
	var secrets corev1.SecretList

	if err := r.List(ctx, &secrets, client.MatchingLabels{helmOwnerLabel: "helm", helmNameLabel: string(detectedCilium)}); err != nil {
		return nil, fmt.Errorf("listing Helm release records: %w", err)
	}

	var out []string

	for i := range secrets.Items {
		if s := &secrets.Items[i]; s.Type == helmReleaseSecretType && !slices.Contains(out, s.Namespace) {
			out = append(out, s.Namespace)
		}
	}

	return out, nil
}

// detectKubeProxy maps the Talos and Cilium sides to spec.cni.kubeProxy. talosEnabled is whether
// Talos runs kube-proxy (KubeProxyConfig.enabled or the inverse of cluster.proxy.disabled; the
// Talos-side read is unit 1). In unmanaged mode only the Talos side counts and the result is the
// recorded setting. In managed mode (Cilium) both sides must agree: disabled with replacement
// off, and enabled with replacement on, are refused.
func detectKubeProxy(f *k8sFacts, talosEnabled, unmanaged bool) (string, error) {
	talosMode := string(cni.KubeProxyReplaced)
	if talosEnabled {
		talosMode = string(cni.KubeProxyEnabled)
	}

	if unmanaged {
		return talosMode, nil
	}

	kpr, ok := f.CiliumConfig[ciliumKPRKey]
	if !ok {
		return "", fmt.Errorf("cilium-config in %s has no %q key; cannot tell whether Cilium replaces kube-proxy",
			k8sFactsSystemNamespace, ciliumKPRKey)
	}

	var replacement bool

	switch strings.ToLower(kpr) {
	case kprTrue, "strict":
		replacement = true
	case "false", "disabled":
		replacement = false
	default:
		return "", fmt.Errorf("cilium-config %s is %q; Ichor adopts only a Cilium that either fully replaces kube-proxy (true) or not at all (false)",
			ciliumKPRKey, kpr)
	}

	switch {
	case talosEnabled && !replacement:
		return string(cni.KubeProxyEnabled), nil
	case !talosEnabled && replacement:
		return string(cni.KubeProxyReplaced), nil
	case !talosEnabled && !replacement:
		return "", fmt.Errorf("kube-proxy is disabled in Talos and Cilium's %s is %q, so nothing routes Services; "+
			"enable one of them deliberately, then run adopt again", ciliumKPRKey, kpr)
	default:
		return "", fmt.Errorf("kube-proxy is enabled in Talos and Cilium's %s is %q, so two components own Service routing; "+
			"make one of them the owner deliberately, then run adopt again", ciliumKPRKey, kpr)
	}
}
