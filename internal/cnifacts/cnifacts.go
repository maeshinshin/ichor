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

// Package cnifacts reads the facts cilium.ValidateValues needs beyond the values themselves (ADR 0099,
// decision 4): hostDNS and KubePrism from the rendered machine configuration, and whether the Gateway
// API CRDs exist. The UI backend (previews) and the controller (before every Cilium Helm Job) build
// the context here, so both apply the same guards.
package cnifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// GatewayAPICRD is the CRD whose presence stands for "the Gateway API CRDs are installed".
const GatewayAPICRD = "gateways.gateway.networking.k8s.io"

const roleControlPlane = "controlplane"

// Source is where the facts are read from.
type Source struct {
	// Client lists the cluster's machines and patches and resolves the render's system inputs.
	Client client.Client
	// CRDs reads the Gateway API CRD by name (an uncached reader needs only get on that one CRD).
	CRDs client.Reader
	// Namespace is where the registry credentials Secrets are read (the controller's namespace).
	Namespace string
	// Bundle is the secrets bundle the machine configuration is rendered with. It is only called when
	// the cluster has an approved control plane.
	Bundle func(context.Context) (*secrets.Bundle, error)
}

// Validate is cilium.ValidateValues on the user values cluster stores, with the facts Context reads,
// for kubeProxy mode (empty: enabled). The controller calls it before it starts a Cilium Helm Job.
func (s Source) Validate(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, kubeProxy cni.KubeProxyMode) error {
	var user map[string]any

	if c := cluster.Spec.CNI.Cilium; c != nil && c.Values != nil && len(c.Values.Raw) > 0 {
		if err := json.Unmarshal(c.Values.Raw, &user); err != nil {
			return fmt.Errorf("reading spec.cni.cilium.values: %w", err)
		}
	}

	if kubeProxy == "" {
		kubeProxy = cni.KubeProxyEnabled
	}

	return cilium.ValidateValues(user, s.Context(ctx, cluster, kubeProxy))
}

// Context collects the facts the guards need. A fact that cannot be read stays nil, which makes its
// guard pass.
func (s Source) Context(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, kubeProxy cni.KubeProxyMode,
) cilium.ValidationContext {
	vc := cilium.ValidationContext{KubeProxy: kubeProxy, Tunnel: cluster.Spec.CNI.Tunnel}

	crd := &metav1.PartialObjectMetadata{}
	crd.SetGroupVersionKind(apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))

	switch err := s.CRDs.Get(ctx, types.NamespacedName{Name: GatewayAPICRD}, crd); {
	case err == nil:
		vc.GatewayAPICRDs = ptr.To(true)
	case apierrors.IsNotFound(err):
		vc.GatewayAPICRDs = ptr.To(false)
	}

	vc.ForwardKubeDNSToHost, vc.KubePrism = s.talosFacts(ctx, cluster)

	return vc
}

// talosFacts renders a control plane's configuration and reads hostDNS and KubePrism from it. Any
// failure leaves both unknown.
func (s Source) talosFacts(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (forward *bool, prism *cilium.KubePrism) {
	var machines ichorv1alpha1.TalosMachineList
	if err := s.Client.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, nil
	}

	i := slices.IndexFunc(machines.Items, func(m ichorv1alpha1.TalosMachine) bool {
		return m.Spec.ClusterRef.Name == cluster.Name && m.Spec.Role == roleControlPlane &&
			m.Spec.ApprovalState == ichorv1alpha1.MachineApprovalStateApproved
	})
	if i < 0 {
		return nil, nil
	}

	bundle, err := s.Bundle(ctx)
	if err != nil {
		return nil, nil
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := s.Client.List(ctx, &patches, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, nil
	}

	sys, err := render.ResolveSystemInputs(ctx, s.Client, s.Namespace, cluster)
	if err != nil {
		return nil, nil
	}

	cfg, err := render.BuildConfig(cluster, &machines.Items[i], bundle, patches.Items, sys)
	if err != nil {
		return nil, nil
	}

	if h := cfg.NetworkHostDNSConfig(); h != nil {
		forward = ptr.To(h.ForwardKubeDNSToHost())
	}

	if kp := cfg.K8sKubePrismConfig(); kp != nil {
		prism = &cilium.KubePrism{Enabled: true, Port: kp.Port()}
	} else {
		prism = &cilium.KubePrism{}
	}

	return forward, prism
}
