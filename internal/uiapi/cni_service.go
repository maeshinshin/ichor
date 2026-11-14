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
	"reflect"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/cnifacts"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/values"
)

// ciliumView is a TalosCluster's Cilium inputs in the form the pkg/cni/cilium functions take.
type ciliumView struct {
	cluster   *ichorv1alpha1.TalosCluster
	kubeProxy cni.KubeProxyMode
	user      map[string]any
	nodes     int // the Nodes the cluster has now (countNodes); 0 unknown
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
		cluster: cluster, kubeProxy: cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy), nodes: s.countNodes(ctx),
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

// validate is every problem cilium.ValidateValues finds in user, with the facts the guards need read
// server-side by the code the controller uses too (cnifacts). The machine
// configuration is rendered with a placeholder bundle and masked Secrets: only hostDNS and KubePrism
// are read from it, never a secret.
func (s *Server) validate(ctx context.Context, v *ciliumView, user map[string]any) []string {
	return flattenErrors(cilium.ValidateValues(user, cnifacts.Source{
		Client: maskedSecrets{s.Client}, CRDs: s.Client, Namespace: s.ControllerNamespace,
		Bundle: func(ctx context.Context) (*secrets.Bundle, error) { return s.renderBundle(ctx, v.cluster) },
	}.Context(ctx, v.cluster, v.kubeProxy)))
}

// setUserValues makes user the cluster's spec.cni.cilium.values (none when empty).
func setUserValues(c *ichorv1alpha1.TalosCluster, user map[string]any) error {
	if len(user) == 0 {
		if c.Spec.CNI.Cilium != nil {
			c.Spec.CNI.Cilium.Values = nil
		}

		return nil
	}

	raw, err := json.Marshal(user)
	if err != nil {
		return err
	}

	if c.Spec.CNI.Cilium == nil {
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{}
	}

	c.Spec.CNI.Cilium.Values = &apiextensionsv1.JSON{Raw: raw}

	return nil
}

// effective is the Helm values the controller would apply for user as the user layer: the same
// function it hashes and applies (cilium.EffectiveValuesForNodes) with the Nodes the cluster has now,
// so a preview is what gets applied.
func (v *ciliumView) effective(user map[string]any) (map[string]any, error) {
	c := v.cluster.DeepCopy()
	if err := setUserValues(c, user); err != nil {
		return nil, err
	}

	return cilium.EffectiveValuesForNodes(c, v.kubeProxy, v.nodes)
}

// countNodes is how many Nodes the cluster has right now, as the controller counts them for the
// Cilium values (clusterNodeCount): all of them, Ready or not. 0 when they cannot be listed (the
// reader may not be allowed to), which gives the desired control-plane count, as before. ichor-ui
// already may list Nodes (GetNode), so this adds no RBAC.
func (s *Server) countNodes(ctx context.Context) int {
	var nodes metav1.PartialObjectMetadataList

	nodes.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("NodeList"))

	if err := s.Client.List(ctx, &nodes); err != nil {
		return 0
	}

	return len(nodes.Items)
}

// mayEditCiliumValues asks, as the caller, for update on talosclusters/cilium-values.
func (s *Server) mayEditCiliumValues(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (bool, error) {
	return s.mayAll(ctx, authv1.ResourceAttributes{
		Namespace: cluster.Namespace, Name: cluster.Name, Group: ichorv1alpha1.GroupVersion.Group, Resource: resourceTalosClusters,
		Verb: ichorv1alpha1.CiliumValuesVerb, Subresource: ichorv1alpha1.CiliumValuesSubresource,
	})
}

// GetCiliumValues returns the layers of a cluster's Cilium values separately.
func (s *Server) GetCiliumValues(
	ctx context.Context, req *connect.Request[pb.GetCiliumValuesRequest],
) (*connect.Response[pb.GetCiliumValuesResponse], error) {
	v, err := s.getCiliumView(ctx, req.Msg.GetCluster())
	if err != nil {
		return nil, err
	}

	defaults, owned, err := cilium.Layers(v.kubeProxy, v.cluster.Spec.CNI.Tunnel, int(v.cluster.Spec.ControlPlane.Count), v.nodes)
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
		// A key Ichor does not set in this mode (the KubePrism address with kube-proxy enabled) shows no value,
		// not "null": it is refused like the rest, but there is nothing for Ichor to set.
		val, set := values.Get(owned, path)

		var value string
		if set {
			y, err := sigsyaml.Marshal(val)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}

			value = strings.TrimSpace(string(y))
		}

		resp.Owned = append(resp.Owned, &pb.OwnedKey{Path: path, Reason: cilium.OwnedKeys[path], ValueYaml: value})
	}

	if v.cluster.Status.CNI != nil {
		resp.AvailableChartVersion = v.cluster.Status.CNI.AvailableChartVersion
	}

	if resp.CanEdit, err = s.mayEditCiliumValues(ctx, v.cluster); err != nil {
		return nil, err
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

	errs := s.validate(ctx, v, user)

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

	// Changing the layer needs the admin's right; the webhook asks the same for a hand-written CR.
	if (len(user) != 0 || len(v.user) != 0) && !reflect.DeepEqual(user, v.user) {
		may, err := s.mayEditCiliumValues(ctx, v.cluster)
		if err != nil {
			return nil, err
		}

		if !may {
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf(
				"you may not change the Cilium values: Kubernetes RBAC does not grant you the %q verb on talosclusters/%s (ichor-admin holds it): "+
					"the chart can run privileged pods on every node",
				ichorv1alpha1.CiliumValuesVerb, ichorv1alpha1.CiliumValuesSubresource))
		}
	}

	if errs := s.validate(ctx, v, user); len(errs) > 0 {
		return nil, invalid("the Cilium values are not valid:\n- %s", strings.Join(errs, "\n- "))
	}

	effective, err := v.effective(user)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	cluster := v.cluster
	if err := setUserValues(cluster, user); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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

	// Cilium moves only to the chart this Ichor embeds, with that chart's images. The request's version was a patch to
	// run on the embedded chart; it is no longer accepted.
	if req.Msg.GetVersion() != "" {
		return nil, invalid("Cilium moves only to the chart this Ichor embeds (%s); a newer Cilium arrives with a newer Ichor release, "+
			"so version %q is not accepted", cilium.ChartVersion, req.Msg.GetVersion())
	}

	if v.cluster.Status.CNI == nil || v.cluster.Status.CNI.AvailableChartVersion == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no Cilium upgrade is available: status.cni.availableChartVersion is empty"))
	}

	if errs := s.validate(ctx, v, v.user); len(errs) > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the stored Cilium values do not validate against the new chart:\n- %s", strings.Join(errs, "\n- ")))
	}

	if blocked, err := s.unfinishedOperation(ctx, v.cluster); err != nil {
		return nil, err
	} else if blocked != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(blocked))
	}

	cluster := v.cluster
	chart := cluster.Status.CNI.AvailableChartVersion

	op := s.newOperation(ctx, cluster, ichorv1alpha1.TalosOperationSpec{
		Kind: ichorv1alpha1.TalosOperationKindCNI,
		CNI:  &ichorv1alpha1.TalosOperationCNISpec{Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: chart},
	})

	if err := s.createOperation(ctx, cluster, op); err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.StartCniUpgradeResponse{
		Operation: objectRef(op.Namespace, op.Name), ToVersion: chart, Audit: s.opAudit(ctx, "create", objectRef(op.Namespace, op.Name)),
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
