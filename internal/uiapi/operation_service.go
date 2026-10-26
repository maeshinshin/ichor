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
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
	"github.com/maeshinshin/ichor/pkg/upgrade"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// OperationService. Every Get/List/Create/Update/Delete here
// is s.X, so with OIDC on it is made as the signed-in user and Kubernetes RBAC
// decides. The only s.Client calls are the tracking check, which
// decides whether a write may happen and returns nothing to the caller.

const (
	opRoleControlPlane = "controlplane"
	opRoleWorker       = "worker"

	kubeProxyEnabled  = "enabled"
	kubeProxyReplaced = "replaced"
)

var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

func kindToSpec(k pb.OperationKind) (string, error) {
	switch k {
	case pb.OperationKind_OPERATION_KIND_TALOS:
		return ichorv1alpha1.TalosOperationKindTalos, nil
	case pb.OperationKind_OPERATION_KIND_KUBERNETES:
		return ichorv1alpha1.TalosOperationKindKubernetes, nil
	case pb.OperationKind_OPERATION_KIND_ICHOR:
		return ichorv1alpha1.TalosOperationKindIchor, nil
	case pb.OperationKind_OPERATION_KIND_CNI:
		return ichorv1alpha1.TalosOperationKindCNI, nil
	case pb.OperationKind_OPERATION_KIND_SHUTDOWN:
		return ichorv1alpha1.TalosOperationKindShutdown, nil
	case pb.OperationKind_OPERATION_KIND_CA_ROTATION:
		return ichorv1alpha1.TalosOperationKindCARotation, nil
	case pb.OperationKind_OPERATION_KIND_CONFIG:
		return ichorv1alpha1.TalosOperationKindConfig, nil
	case pb.OperationKind_OPERATION_KIND_UNSPECIFIED:
	}

	return "", connect.NewError(connect.CodeInvalidArgument, errors.New("kind is required"))
}

func kindToPB(k string) pb.OperationKind {
	switch k {
	case ichorv1alpha1.TalosOperationKindTalos:
		return pb.OperationKind_OPERATION_KIND_TALOS
	case ichorv1alpha1.TalosOperationKindKubernetes:
		return pb.OperationKind_OPERATION_KIND_KUBERNETES
	case ichorv1alpha1.TalosOperationKindIchor:
		return pb.OperationKind_OPERATION_KIND_ICHOR
	case ichorv1alpha1.TalosOperationKindCNI:
		return pb.OperationKind_OPERATION_KIND_CNI
	case ichorv1alpha1.TalosOperationKindShutdown:
		return pb.OperationKind_OPERATION_KIND_SHUTDOWN
	case ichorv1alpha1.TalosOperationKindCARotation:
		return pb.OperationKind_OPERATION_KIND_CA_ROTATION
	case ichorv1alpha1.TalosOperationKindConfig:
		return pb.OperationKind_OPERATION_KIND_CONFIG
	}

	return pb.OperationKind_OPERATION_KIND_UNSPECIFIED
}

func operationToPB(op *ichorv1alpha1.TalosOperation) *pb.Operation {
	out := &pb.Operation{
		Ref:            objectRef(op.Namespace, op.Name),
		Cluster:        objectRef(op.Namespace, op.Spec.ClusterRef.Name),
		Kind:           kindToPB(op.Spec.Kind),
		Paused:         op.Spec.Paused,
		MaxUnavailable: op.Spec.MaxUnavailable,
		Phase:          op.Status.Phase,
		FailureReason:  op.Status.FailureReason,
		Conditions:     conditions(op.Status.Conditions),
		CreatedAt:      timestamppb.New(op.CreationTimestamp.Time),
	}

	if op.Status.CARotation != nil {
		out.CaRotationStage = op.Status.CARotation.Stage
	}

	for _, m := range op.Status.Machines {
		out.Machines = append(out.Machines, &pb.OperationMachine{Name: m.Name, Phase: m.Phase})
	}

	return out
}

func mapOperationWriteError(verb string, key types.NamespacedName, err error) error {
	what := fmt.Errorf("%s %q: %w", verb, key, err)

	switch {
	case apierrors.IsForbidden(err):
		return connect.NewError(connect.CodePermissionDenied, what)
	case apierrors.IsNotFound(err):
		return connect.NewError(connect.CodeNotFound, what)
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return connect.NewError(connect.CodeInvalidArgument, what)
	case apierrors.IsAlreadyExists(err):
		return connect.NewError(connect.CodeAlreadyExists, what)
	case apierrors.IsConflict(err):
		return connect.NewError(connect.CodeAborted, what)
	default:
		return connect.NewError(connect.CodeInternal, what)
	}
}

// ListOperations returns the TalosOperations the caller may list, optionally of one cluster.
func (s *Server) ListOperations(
	ctx context.Context, req *connect.Request[pb.ListOperationsRequest],
) (*connect.Response[pb.ListOperationsResponse], error) {
	var list ichorv1alpha1.TalosOperationList
	if err := s.List(ctx, &list); err != nil {
		return nil, mapListError("operations", err)
	}

	filter := req.Msg.GetCluster()
	out := &pb.ListOperationsResponse{}

	for i := range list.Items {
		op := &list.Items[i]
		if filter != nil && (op.Namespace != filter.GetNamespace() || op.Spec.ClusterRef.Name != filter.GetName()) {
			continue
		}

		out.Operations = append(out.Operations, operationToPB(op))
	}

	return connect.NewResponse(out), nil
}

func (s *Server) getOperation(ctx context.Context, ref *pb.ObjectRef) (*ichorv1alpha1.TalosOperation, error) {
	var op ichorv1alpha1.TalosOperation

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &op); err != nil {
		return nil, mapGetError(fmt.Sprintf("operation %q", key), err)
	}

	return &op, nil
}

// GetOperation returns one operation with its status as the controller wrote it.
func (s *Server) GetOperation(
	ctx context.Context, req *connect.Request[pb.GetOperationRequest],
) (*connect.Response[pb.GetOperationResponse], error) {
	op, err := s.getOperation(ctx, req.Msg.GetOperation())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.GetOperationResponse{Operation: operationToPB(op)}), nil
}

// SetOperationPaused writes spec.paused. It does nothing else: pausing keeps
// the cluster lock and undoes nothing.
func (s *Server) SetOperationPaused(
	ctx context.Context, req *connect.Request[pb.SetOperationPausedRequest],
) (*connect.Response[pb.SetOperationPausedResponse], error) {
	ref := req.Msg.GetOperation()

	op, err := s.getOperation(ctx, ref)
	if err != nil {
		return nil, err
	}

	op.Spec.Paused = req.Msg.GetPaused()

	if err := s.Update(ctx, op, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("updating operation", namespacedName(ref), err)
	}

	action := "resume"
	if op.Spec.Paused {
		action = "pause"
	}

	return connect.NewResponse(&pb.SetOperationPausedResponse{Operation: operationToPB(op), Audit: s.opAudit(ctx, action, ref)}), nil
}

// DeleteOperation removes the record. It does not stop a step already sent to
// a machine; the cluster lock is released by the controller.
func (s *Server) DeleteOperation(
	ctx context.Context, req *connect.Request[pb.DeleteOperationRequest],
) (*connect.Response[pb.DeleteOperationResponse], error) {
	ref := req.Msg.GetOperation()

	op, err := s.getOperation(ctx, ref)
	if err != nil {
		return nil, err
	}

	if err := s.Delete(ctx, op); err != nil {
		return nil, mapOperationWriteError("deleting operation", namespacedName(ref), err)
	}

	return connect.NewResponse(&pb.DeleteOperationResponse{Audit: s.opAudit(ctx, "delete", ref)}), nil
}

func (s *Server) opAudit(ctx context.Context, action string, target *pb.ObjectRef) *pb.AuditRecord {
	return &pb.AuditRecord{Actor: s.actorName(ctx), At: timestamppb.New(s.now()), Action: action, Target: target}
}

// opPlan is PlanOperation's result plus what CreateOperation needs to act on it.
type opPlan struct {
	resp    *pb.PlanOperationResponse
	kind    string
	cluster *ichorv1alpha1.TalosCluster
	// install is set for the ichor kind.
	install *ichorv1alpha1.IchorInstallation
}

// normalizeTarget validates target for kind and returns it in the form the spec stores.
func normalizeTarget(kind, target string) (string, error) {
	switch kind {
	case ichorv1alpha1.TalosOperationKindShutdown, ichorv1alpha1.TalosOperationKindCARotation, ichorv1alpha1.TalosOperationKindConfig:
		if target != "" {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("a %s has no target", kind))
		}
	case ichorv1alpha1.TalosOperationKindCNI:
		if target != "" && target != kubeProxyEnabled && target != kubeProxyReplaced {
			return "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("kube-proxy mode %q is not %q or %q", target, kubeProxyEnabled, kubeProxyReplaced))
		}
	default:
		if target == "" {
			return "", nil
		}

		if !versionPattern.MatchString(target) {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("version %q is not a semantic version like v1.2.3", target))
		}

		return "v" + strings.TrimPrefix(target, "v"), nil
	}

	return target, nil
}

// trackedBy names the tool tracking obj, or "". A spec write to a tracked CR
// belongs in its repository; "cannot tell" refuses too.
func (s *Server) trackedBy(ctx context.Context, obj client.Object) (string, error) {
	tracked, by, err := gitopstracking.IsTracked(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		return "", connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("cannot tell whether Argo CD or Flux tracks %q, so it is not written: %w", client.ObjectKeyFromObject(obj), err))
	}

	if !tracked {
		return "", nil
	}

	return by, nil
}

//nolint:gocyclo // one switch over the seven kinds, each a few lines
func (s *Server) planOperation(
	ctx context.Context, clusterRef *pb.ObjectRef, pbKind pb.OperationKind, rawTarget string, maxUnavailable int32,
) (*opPlan, error) {
	kind, err := kindToSpec(pbKind)
	if err != nil {
		return nil, err
	}

	target, err := normalizeTarget(kind, rawTarget)
	if err != nil {
		return nil, err
	}

	if maxUnavailable < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("max_unavailable cannot be negative"))
	}

	// A CA rotation visits the machines one at a time, control planes first.
	if maxUnavailable == 0 || kind == ichorv1alpha1.TalosOperationKindCARotation {
		maxUnavailable = 1
	}

	var cluster ichorv1alpha1.TalosCluster

	key := namespacedName(clusterRef)
	if err := s.Get(ctx, key, &cluster); err != nil {
		return nil, mapGetError(fmt.Sprintf("cluster %q", key), err)
	}

	p := &opPlan{kind: kind, cluster: &cluster, resp: &pb.PlanOperationResponse{MaxUnavailable: maxUnavailable}}

	var tracked client.Object = &cluster

	switch kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		p.resp.Effect, p.resp.Current = pb.OperationEffect_OPERATION_EFFECT_REBOOT, cluster.Spec.TalosVersion
	case ichorv1alpha1.TalosOperationKindKubernetes:
		p.resp.Effect, p.resp.Current = pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS, cluster.Spec.KubernetesVersion
	case ichorv1alpha1.TalosOperationKindCNI:
		p.resp.Effect, p.resp.Current = pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS, cluster.Spec.CNI.KubeProxy
		if p.resp.Current == "" {
			p.resp.Current = kubeProxyEnabled
		}
	case ichorv1alpha1.TalosOperationKindShutdown:
		p.resp.Effect = pb.OperationEffect_OPERATION_EFFECT_STOP
	case ichorv1alpha1.TalosOperationKindConfig:
		p.resp.Effect = pb.OperationEffect_OPERATION_EFFECT_REBOOT
	case ichorv1alpha1.TalosOperationKindCARotation:
		p.resp.Effect = pb.OperationEffect_OPERATION_EFFECT_CA_ROTATION
		p.resp.Stages = []string{
			ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping,
		}
		p.resp.ReplacesSecretsBundle = true
	case ichorv1alpha1.TalosOperationKindIchor:
		p.resp.Effect = pb.OperationEffect_OPERATION_EFFECT_NONE
		p.install = &ichorv1alpha1.IchorInstallation{}

		if err := s.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, p.install); err != nil {
			return nil, mapGetError("IchorInstallation \""+ichorv1alpha1.IchorInstallationName+"\"", err)
		}

		p.resp.Current, tracked = p.install.Spec.Version, p.install
	}

	p.resp.Target = target
	p.resp.SpecChanges = target != "" && target != p.resp.Current

	if err := s.planMachines(ctx, p, &cluster, maxUnavailable); err != nil {
		return nil, err
	}

	p.resp.Blocked, err = s.blockedReason(ctx, p, &cluster, tracked)
	if err != nil {
		return nil, err
	}

	return p, nil
}

func (s *Server) planMachines(ctx context.Context, p *opPlan, cluster *ichorv1alpha1.TalosCluster, maxUnavailable int32) error {
	if p.kind == ichorv1alpha1.TalosOperationKindIchor {
		return nil
	}

	var list ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &list, client.InNamespace(cluster.Namespace)); err != nil {
		return mapListError("machines", err)
	}

	var cps, workers []*ichorv1alpha1.TalosMachine

	approvedControlPlanes := 0

	for i := range list.Items {
		m := &list.Items[i]
		if m.Spec.ClusterRef.Name != cluster.Name || !m.Spec.IsApproved() {
			continue
		}

		if m.Spec.Role == opRoleControlPlane {
			approvedControlPlanes++
		}

		// A config rollout visits only the machines with a pending change that needs a reboot
		// (TalosMachine.status.rebootPaths).
		if p.kind == ichorv1alpha1.TalosOperationKindConfig && len(m.Status.RebootPaths) == 0 {
			continue
		}

		// A hold excludes a machine from a rollout, not from stopping the cluster and not from
		// a CA rotation, which would lock a machine left out when the old CA is dropped.
		if p.kind != ichorv1alpha1.TalosOperationKindShutdown && p.kind != ichorv1alpha1.TalosOperationKindCARotation && m.Annotations[ichorv1alpha1.TalosMachineUpgradeHoldAnnotation] != "" {
			p.resp.Held = append(p.resp.Held, m.Name)

			continue
		}

		if m.Spec.Role == opRoleControlPlane {
			cps = append(cps, m)
		} else {
			workers = append(workers, m)
		}
	}

	slices.Sort(p.resp.Held)

	// With one approved control plane, rebooting it takes the Kubernetes API down for the length of
	// the reboot (the cluster has no other API server to answer).
	p.resp.KubernetesApiUnavailable = p.resp.Effect == pb.OperationEffect_OPERATION_EFFECT_REBOOT &&
		approvedControlPlanes == 1 && len(cps) == 1

	add := func(m *ichorv1alpha1.TalosMachine, step int32) {
		pm := &pb.PlannedMachine{Name: m.Name, Role: m.Spec.Role, Step: step}
		if p.kind == ichorv1alpha1.TalosOperationKindConfig {
			pm.RebootFields = slices.Clone(m.Status.RebootPaths)
		}

		p.resp.Machines = append(p.resp.Machines, pm)
	}

	if p.kind == ichorv1alpha1.TalosOperationKindShutdown {
		// Workers together first, then every control plane in one burst, each in the executor's order.
		upgrade.SortByOrder(workers)
		upgrade.SortByOrder(cps)

		for _, m := range workers {
			add(m, 1)
		}

		for _, m := range cps {
			add(m, 2)
		}

		return nil
	}

	upgrade.SortByOrder(cps)
	upgrade.SortByOrder(workers)

	step := int32(0)

	for _, m := range cps {
		step++
		add(m, step)
	}

	for i, m := range workers {
		if int32(i)%maxUnavailable == 0 {
			step++
		}

		add(m, step)
	}

	return nil
}

func (s *Server) blockedReason(
	ctx context.Context, p *opPlan, cluster *ichorv1alpha1.TalosCluster, tracked client.Object,
) (string, error) {
	var ops ichorv1alpha1.TalosOperationList
	if err := s.List(ctx, &ops, client.InNamespace(cluster.Namespace)); err != nil {
		return "", mapListError("operations", err)
	}

	for i := range ops.Items {
		o := &ops.Items[i]
		if o.Spec.ClusterRef.Name == cluster.Name && o.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
			return fmt.Sprintf("operation %q (%s) on this cluster is not finished (phase %q); wait for it, or delete it if it is stuck",
				o.Name, o.Spec.Kind, o.Status.Phase), nil
		}
	}

	if p.kind == ichorv1alpha1.TalosOperationKindConfig && len(p.resp.Machines) == 0 {
		return "no approved machine has a pending configuration change that needs a reboot (nothing is waiting in Ready=RebootRequired)", nil
	}

	if p.resp.SpecChanges {
		by, err := s.trackedBy(ctx, tracked)
		if err != nil {
			return "", err
		}

		if by != "" {
			what := "TalosCluster"
			if p.install != nil {
				what = "IchorInstallation"
			}

			return fmt.Sprintf("%s %q is tracked by %s, so its spec is read-only here: change it in Git (a pull request), "+
				"then start the operation without a target", what, tracked.GetName(), by), nil
		}
	}

	return "", nil
}

// PlanOperation reports what CreateOperation would do, without writing.
func (s *Server) PlanOperation(
	ctx context.Context, req *connect.Request[pb.PlanOperationRequest],
) (*connect.Response[pb.PlanOperationResponse], error) {
	p, err := s.planOperation(ctx, req.Msg.GetCluster(), req.Msg.GetKind(), req.Msg.GetTarget(), req.Msg.GetMaxUnavailable())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(p.resp), nil
}

// CreateOperation changes the spec to the target (when one is given and
// differs) and then creates the TalosOperation. The spec goes first because
// the executors read the target from it when a machine's turn comes: an
// operation created first would run against the old version and finish
// having done nothing. Everything that can be refused is refused before the
// first write -- the plan's blockers, and a dry-run of the operation's
// creation as the caller, so a caller who may edit a cluster but may not
// create operations leaves its spec alone.
func (s *Server) CreateOperation(
	ctx context.Context, req *connect.Request[pb.CreateOperationRequest],
) (*connect.Response[pb.CreateOperationResponse], error) {
	m := req.Msg

	p, err := s.planOperation(ctx, m.GetCluster(), m.GetKind(), m.GetTarget(), m.GetMaxUnavailable())
	if err != nil {
		return nil, err
	}

	if p.resp.GetBlocked() != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(p.resp.GetBlocked()))
	}

	cluster := p.cluster
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: p.kind + "-", Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: p.kind, MaxUnavailable: m.GetMaxUnavailable(),
		},
	}
	clusterKey := client.ObjectKeyFromObject(cluster)

	if err := s.Create(ctx, op.DeepCopy(), client.DryRunAll, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("creating operation for", clusterKey, err)
	}

	if p.resp.GetSpecChanges() {
		if err := s.writeTarget(ctx, p); err != nil {
			return nil, err
		}
	}

	if err := s.Create(ctx, op, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("creating operation for", clusterKey, err)
	}

	return connect.NewResponse(&pb.CreateOperationResponse{
		Operation: operationToPB(op), Audit: s.opAudit(ctx, "create", objectRef(op.Namespace, op.Name)), SpecChanged: p.resp.GetSpecChanges(),
	}), nil
}

// writeTarget stores the target in the spec it belongs to.
func (s *Server) writeTarget(ctx context.Context, p *opPlan) error {
	target := p.resp.GetTarget()
	cluster := p.cluster
	key := client.ObjectKeyFromObject(cluster)

	switch p.kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		cluster.Spec.TalosVersion = target
	case ichorv1alpha1.TalosOperationKindKubernetes:
		cluster.Spec.KubernetesVersion = target
	case ichorv1alpha1.TalosOperationKindIchor:
		p.install.Spec.Version = target

		if err := s.Update(ctx, p.install, strictFieldValidation); err != nil {
			return mapOperationWriteError("updating IchorInstallation", client.ObjectKeyFromObject(p.install), err)
		}

		return nil
	case ichorv1alpha1.TalosOperationKindCNI:
		// the admission policy denies a kubeProxy change without this
		// annotation. It is set for the one write that changes the mode and
		// removed again at once, so it never lingers as a standing consent for
		// someone else's later edit.
		if cluster.Annotations == nil {
			cluster.Annotations = map[string]string{}
		}

		cluster.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation] = "ichor-" + s.actorName(ctx)
		cluster.Spec.CNI.KubeProxy = target
	}

	if err := s.Update(ctx, cluster, strictFieldValidation); err != nil {
		return mapOperationWriteError("updating cluster", key, err)
	}

	if p.kind == ichorv1alpha1.TalosOperationKindCNI {
		delete(cluster.Annotations, ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation)

		if err := s.Update(ctx, cluster, strictFieldValidation); err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("kube-proxy mode is now %q but the annotation %s could not be removed, "+
				"so no operation was created: remove it and start the operation without a target: %w",
				target, ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation, err))
		}
	}

	return nil
}
