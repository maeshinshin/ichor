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

package uiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/pmezard/go-difflib/difflib"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/values"
)

// gatewayAPICRD is the CRD whose presence stands for "the Gateway API CRDs are installed".
const gatewayAPICRD = "gateways.gateway.networking.k8s.io"

// ciliumView is a TalosCluster's Cilium inputs in the form the pkg/cni/cilium functions take.
type ciliumView struct {
	cluster   *ichorv1alpha1.TalosCluster
	kubeProxy cni.KubeProxyMode
	tunnel    string
	user      map[string]any
}

func (s *Server) getCiliumView(ctx context.Context, ref *pb.ObjectRef) (*ciliumView, error) {
	key := namespacedName(ref)

	cluster, err := s.loadCluster(ctx, key)
	if err != nil {
		return nil, err
	}

	if cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(ichorv1alpha1.CNINotManagedMessage))
	}

	v := &ciliumView{
		cluster: cluster, kubeProxy: cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy), tunnel: cluster.Spec.CNI.Tunnel,
	}
	if v.kubeProxy == "" {
		v.kubeProxy = cni.KubeProxyEnabled
	}

	if c := cluster.Spec.CNI.Cilium; c != nil && c.Values != nil && len(c.Values.Raw) > 0 {
		if err := json.Unmarshal(c.Values.Raw, &v.user); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("cluster %q: reading spec.cni.cilium.values: %w", key, err))
		}
	}

	return v, nil
}

// parseUserYAML reads a proposed user layer. Empty text is an empty layer.
func parseUserYAML(text string) (map[string]any, error) {
	var user map[string]any
	if err := sigsyaml.Unmarshal([]byte(text), &user); err != nil {
		return nil, invalid("user_yaml is not a YAML mapping: %w", err)
	}

	return user, nil
}

func toYAML(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "", nil
	}

	out, err := sigsyaml.Marshal(m)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}

	return string(out), nil
}

// flattenErrors lists every leaf of a (nested) errors.Join.
func flattenErrors(err error) []string {
	if err == nil {
		return nil
	}

	if j, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // only a join is split
		var out []string

		for _, e := range j.Unwrap() {
			out = append(out, flattenErrors(e)...)
		}

		return out
	}

	return []string{err.Error()}
}

// validationContext collects the facts the guards need, read server-side. A fact that cannot be
// read stays nil, which makes its guard pass: the controller re-checks live before every Job.
func (s *Server) validationContext(ctx context.Context, v *ciliumView) cilium.ValidationContext {
	vc := cilium.ValidationContext{KubeProxy: v.kubeProxy, Tunnel: v.tunnel}

	crd := &metav1.PartialObjectMetadata{}
	crd.SetGroupVersionKind(apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))

	switch err := s.Client.Get(ctx, types.NamespacedName{Name: gatewayAPICRD}, crd); {
	case err == nil:
		vc.GatewayAPICRDs = ptr.To(true)
	case apierrors.IsNotFound(err):
		vc.GatewayAPICRDs = ptr.To(false)
	}

	vc.ForwardKubeDNSToHost, vc.KubePrism = s.talosFacts(ctx, v.cluster)

	return vc
}

// talosFacts renders a control plane's configuration, as GetEffectiveConfig does, and reads
// hostDNS and KubePrism from it. Any failure leaves both unknown.
func (s *Server) talosFacts(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (forward *bool, prism *cilium.KubePrism) {
	var machines ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, nil
	}

	i := slices.IndexFunc(machines.Items, func(m ichorv1alpha1.TalosMachine) bool {
		return m.Spec.ClusterRef.Name == cluster.Name && m.Spec.Role == opRoleControlPlane &&
			m.Spec.ApprovalState == ichorv1alpha1.MachineApprovalStateApproved
	})
	if i < 0 {
		return nil, nil
	}

	bundle, err := s.renderBundle(ctx, cluster)
	if err != nil {
		return nil, nil
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := s.List(ctx, &patches, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, nil
	}

	sys, err := render.ResolveSystemInputs(ctx, maskedSecrets{s.Client}, s.ControllerNamespace, cluster)
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

func (v *ciliumView) effective(user map[string]any) (map[string]any, error) {
	return cilium.EffectiveValues(user, v.kubeProxy, v.tunnel, int(v.cluster.Spec.ControlPlane.Count))
}

// GetCiliumValues returns the layers of a cluster's Cilium values separately.
func (s *Server) GetCiliumValues(
	ctx context.Context, req *connect.Request[pb.GetCiliumValuesRequest],
) (*connect.Response[pb.GetCiliumValuesResponse], error) {
	v, err := s.getCiliumView(ctx, req.Msg.GetCluster())
	if err != nil {
		return nil, err
	}

	defaults, owned, err := cilium.Layers(v.kubeProxy, v.tunnel, int(v.cluster.Spec.ControlPlane.Count))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	effective, err := v.effective(v.user)
	if err != nil {
		// A stored layer that no longer passes (an owned key written around the webhook): show the
		// layers Ichor knows, and leave the effective values empty rather than hide the layer.
		effective = nil
	}

	resp := &pb.GetCiliumValuesResponse{}
	if resp.DefaultsYaml, err = toYAML(defaults); err != nil {
		return nil, err
	}

	if resp.UserYaml, err = toYAML(v.user); err != nil {
		return nil, err
	}

	if resp.EffectiveYaml, err = toYAML(effective); err != nil {
		return nil, err
	}

	for _, path := range slices.Sorted(maps.Keys(cilium.OwnedKeys)) {
		val, _ := values.Get(owned, path)

		y, err := sigsyaml.Marshal(val)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		resp.Owned = append(resp.Owned, &pb.OwnedKey{Path: path, Reason: cilium.OwnedKeys[path], ValueYaml: strings.TrimSpace(string(y))})
	}

	if v.cluster.Status.CNI != nil {
		resp.AvailableChartVersion = v.cluster.Status.CNI.AvailableChartVersion
	}

	return connect.NewResponse(resp), nil
}

// ValidateCiliumValues checks a proposed user layer and returns every problem.
func (s *Server) ValidateCiliumValues(
	ctx context.Context, req *connect.Request[pb.ValidateCiliumValuesRequest],
) (*connect.Response[pb.ValidateCiliumValuesResponse], error) {
	v, err := s.getCiliumView(ctx, req.Msg.GetCluster())
	if err != nil {
		return nil, err
	}

	user, err := parseUserYAML(req.Msg.GetUserYaml())
	if err != nil {
		return nil, err
	}

	errs := flattenErrors(cilium.ValidateValues(user, s.validationContext(ctx, v)))

	return connect.NewResponse(&pb.ValidateCiliumValuesResponse{Valid: len(errs) == 0, Errors: errs}), nil
}

// DiffCiliumValues compares the effective values now with those a proposed user layer gives.
func (s *Server) DiffCiliumValues(
	ctx context.Context, req *connect.Request[pb.DiffCiliumValuesRequest],
) (*connect.Response[pb.DiffCiliumValuesResponse], error) {
	v, err := s.getCiliumView(ctx, req.Msg.GetCluster())
	if err != nil {
		return nil, err
	}

	user, err := parseUserYAML(req.Msg.GetUserYaml())
	if err != nil {
		return nil, err
	}

	current, err := v.effective(v.user)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the stored values do not give effective values: %w", err))
	}

	proposed, err := v.effective(user)
	if err != nil {
		return nil, invalid("the proposed values do not give effective values: %w", err)
	}

	a, err := toYAML(current)
	if err != nil {
		return nil, err
	}

	b, err := toYAML(proposed)
	if err != nil {
		return nil, err
	}

	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(a), B: difflib.SplitLines(b), FromFile: "current", ToFile: "proposed", Context: 3,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&pb.DiffCiliumValuesResponse{Diff: diff}), nil
}

// SetCiliumValues writes spec.cni.cilium.values: validated first, dry-run before the real write. The controller applies it.
func (s *Server) SetCiliumValues(
	ctx context.Context, req *connect.Request[pb.SetCiliumValuesRequest],
) (*connect.Response[pb.SetCiliumValuesResponse], error) {
	ref := req.Msg.GetCluster()

	v, err := s.getCiliumView(ctx, ref)
	if err != nil {
		return nil, err
	}

	user, err := parseUserYAML(req.Msg.GetUserYaml())
	if err != nil {
		return nil, err
	}

	if errs := flattenErrors(cilium.ValidateValues(user, s.validationContext(ctx, v))); len(errs) > 0 {
		return nil, invalid("the Cilium values are not valid:\n- %s", strings.Join(errs, "\n- "))
	}

	effective, err := v.effective(user)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cluster := v.cluster

	switch {
	case len(user) == 0 && cluster.Spec.CNI.Cilium != nil:
		cluster.Spec.CNI.Cilium.Values = nil
	case len(user) > 0:
		raw, err := json.Marshal(user)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		if cluster.Spec.CNI.Cilium == nil {
			cluster.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{}
		}

		cluster.Spec.CNI.Cilium.Values = &apiextensionsv1.JSON{Raw: raw}
	}

	key := client.ObjectKeyFromObject(cluster)

	if err := s.Update(ctx, cluster.DeepCopy(), client.DryRunAll, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("updating cluster", key, err)
	}

	if err := s.Update(ctx, cluster, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("updating cluster", key, err)
	}

	out, err := toYAML(effective)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.SetCiliumValuesResponse{
		Audit:         s.opAudit(ctx, verbUpdate, ref),
		EffectiveYaml: out,
	}), nil
}

// StartCniUpgrade creates the cni TalosOperation that upgrades the Cilium release to the chart this
// Ichor embeds (spec.cni.action upgrade). It first checks that an upgrade is on offer, that
// nothing else is running on the cluster and that the stored user layer still validates; the
// creation is dry-run before it is real, as the caller, so a caller who may not create operations
// starts nothing.
func (s *Server) StartCniUpgrade(
	ctx context.Context, req *connect.Request[pb.StartCniUpgradeRequest],
) (*connect.Response[pb.StartCniUpgradeResponse], error) {
	v, err := s.getCiliumView(ctx, req.Msg.GetCluster())
	if err != nil {
		return nil, err
	}

	// A patch is the one the server offers as a cni patch: its images
	// verified and it is newer than the chart this Ichor embeds. The release moves to the embedded chart
	// with the patch's images; the controller checks the signatures before the Job runs them.
	version := req.Msg.GetVersion()
	overridden := ""

	if version == "" {
		if v.cluster.Status.CNI == nil || v.cluster.Status.CNI.AvailableChartVersion == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no Cilium upgrade is available: status.cni.availableChartVersion is empty"))
		}
	} else {
		offered, compat, reason, err := s.ciliumPatchOffer(ctx, v.cluster, version)
		if err != nil {
			return nil, err
		}

		if !offered {
			// Only an incompatibility with the cluster's Kubernetes is overridable; an unverified patch never is.
			overridden, err = s.ciliumCompatOverride(ctx, v.cluster, version, compat, req.Msg.GetOverride())
			if err != nil {
				return nil, err
			}

			if overridden == "" {
				why := "it is not a verified newer patch of the running Cilium minor"
				if reason != "" {
					why = reason
				}

				return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the Cilium patch %s is not offered: %s", version, why))
			}
		}
	}

	if errs := flattenErrors(cilium.ValidateValues(v.user, s.validationContext(ctx, v))); len(errs) > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the stored Cilium values do not validate against the new chart:\n- %s", strings.Join(errs, "\n- ")))
	}

	if blocked, err := s.unfinishedOperation(ctx, v.cluster); err != nil {
		return nil, err
	} else if blocked != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(blocked))
	}

	// The chart the release moves to: the one the status says is available, or for a patch the embedded
	// chart the patch rides on.
	cluster := v.cluster
	chart := cilium.ChartVersion
	if version == "" {
		chart = cluster.Status.CNI.AvailableChartVersion
	}

	to := version
	if to == "" {
		to = chart
	}

	op := &ichorv1alpha1.TalosOperation{
		GenerateName: ichorv1alpha1.TalosOperationKindCNI + "-", Namespace: cluster.Namespace, Annotations: s.startedByAnnotation(ctx),
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Kind:       ichorv1alpha1.TalosOperationKindCNI,
			CNI: &ichorv1alpha1.TalosOperationCNISpec{
				Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: chart, ImageVersion: version,
			},
		},
	}

	if err := s.createOperation(ctx, cluster, op); err != nil {
		return nil, err
	}

	// A patch that went through under the override verb is recorded in the cluster's Events.
	if overridden != "" {
		if err := s.emitWarningEvent(ctx, cluster, kindTalosCluster, "Override", overridden); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	return connect.NewResponse(&pb.StartCniUpgradeResponse{
		Operation: objectRef(op.Namespace, op.Name), ToVersion: to, Audit: s.opAudit(ctx, "create", objectRef(op.Namespace, op.Name)),
	}), nil
}

// overridableKey is the response header of a refusal the override verb lifts: the UI shows its override option
// for such a refusal only.
const (
	overridableKey   = "Ichor-Overridable"
	overridableValue = "true"
)

// overridableRefusal is a FailedPrecondition the override verb lifts, marked so the UI offers its override option.
func overridableRefusal(err error) *connect.Error {
	ce := connect.NewError(connect.CodeFailedPrecondition, err)
	ce.Meta().Set(overridableKey, overridableValue)

	return ce
}

// ciliumCompatOverride handles a patch the server does not offer. With no overridable incompatibility (compat nil) it
// returns "" and the caller refuses as before. With one, and without an override, it refuses with overridableKey set;
// with an override it checks the caller holds the verb on the cluster and returns the text for the Event.
func (s *Server) ciliumCompatOverride(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, version string, compat error, bg *pb.Override,
) (string, error) {
	if compat == nil {
		return "", nil
	}

	if bg == nil {
		return "", overridableRefusal(fmt.Errorf("the Cilium patch %s is incompatible: %w; the override verb can lift this", version, compat))
	}

	if err := s.authorizeOverride(ctx, cluster); err != nil {
		return "", err
	}

	return fmt.Sprintf("cilium %s is incompatible (%v), overridden under override: %s", version, compat, overrideReason(bg.GetReason())), nil
}
