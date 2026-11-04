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
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/release"
	"github.com/maeshinshin/ichor/pkg/upgrade"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// OperationService (docs/adr/0068). Every Get/List/Create/Update/Delete here
// is s.X, so with OIDC on it is made as the signed-in user and Kubernetes RBAC
// decides (ADR 0058). The only s.Client calls are the tracking check, which
// decides whether a write may happen and returns nothing to the caller.

const (
	opRoleControlPlane = "controlplane"
	opRoleWorker       = "worker"

	kubeProxyEnabled  = "enabled"
	kubeProxyReplaced = "replaced"
)

// opKinds pairs each wire kind with the spec string it is stored as.
var opKinds = []struct {
	pb   pb.OperationKind
	spec string
}{
	{pb.OperationKind_OPERATION_KIND_TALOS, ichorv1alpha1.TalosOperationKindTalos},
	{pb.OperationKind_OPERATION_KIND_KUBERNETES, ichorv1alpha1.TalosOperationKindKubernetes},
	{pb.OperationKind_OPERATION_KIND_ICHOR, ichorv1alpha1.TalosOperationKindIchor},
	{pb.OperationKind_OPERATION_KIND_CNI, ichorv1alpha1.TalosOperationKindCNI},
	{pb.OperationKind_OPERATION_KIND_SHUTDOWN, ichorv1alpha1.TalosOperationKindShutdown},
	{pb.OperationKind_OPERATION_KIND_CA_ROTATION, ichorv1alpha1.TalosOperationKindCARotation},
	{pb.OperationKind_OPERATION_KIND_CONFIG, ichorv1alpha1.TalosOperationKindConfig},
	{pb.OperationKind_OPERATION_KIND_NODE, ichorv1alpha1.TalosOperationKindNode},
	{pb.OperationKind_OPERATION_KIND_ETCD_RESTORE, ichorv1alpha1.TalosOperationKindEtcdRestore},
}

func kindToSpec(k pb.OperationKind) (string, error) {
	for _, e := range opKinds {
		if e.pb == k {
			return e.spec, nil
		}
	}

	return "", invalid("kind is required")
}

func kindToPB(k string) pb.OperationKind {
	for _, e := range opKinds {
		if e.spec == k {
			return e.pb
		}
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
		Message:        op.Status.Message,
		Conditions:     conditions(op.Status.Conditions),
		CreatedAt:      timestamppb.New(op.CreationTimestamp.Time),
	}

	if op.Status.CARotation != nil {
		out.CaRotationStage = op.Status.CARotation.Stage
	}

	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation {
		out.CaTargets = caRotationTargets(op)
	}

	if op.Status.Kubernetes != nil {
		out.KubernetesStage = op.Status.Kubernetes.Stage
	}

	if n := op.Spec.Node; n != nil {
		out.NodeAction = nodeActionToPB(n.Action)
		out.NodeMachine = objectRef(op.Namespace, n.MachineRef.Name)

		if op.Status.Node != nil {
			out.NodeMessage = op.Status.Node.Message
		}
	}

	etcdRestoreToPB(out, op)

	for _, m := range op.Status.Machines {
		out.Machines = append(out.Machines, operationMachineToPB(op, m))
	}

	describeOperation(out, op, nil, nil)

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

	out := operationToPB(op)
	s.enrichOperation(ctx, out, op)

	return connect.NewResponse(&pb.GetOperationResponse{Operation: out}), nil
}

// SetOperationPaused writes spec.paused: pausing keeps the cluster lock and undoes nothing (ADR 0026).
// Resuming an operation that paused itself after a failed step also sets the retry annotation to a time
// newer than status.lastRetry, so the controller runs the failed step again (ADR 0095).
func (s *Server) SetOperationPaused(
	ctx context.Context, req *connect.Request[pb.SetOperationPausedRequest],
) (*connect.Response[pb.SetOperationPausedResponse], error) {
	ref := req.Msg.GetOperation()

	op, err := s.getOperation(ctx, ref)
	if err != nil {
		return nil, err
	}

	if req.Msg.GetPaused() {
		if err := etcdRestoreHoldsPause(op); err != nil {
			return nil, err
		}
	}

	op.Spec.Paused = req.Msg.GetPaused()

	if !op.Spec.Paused && op.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && op.Status.FailureReason != "" {
		at := time.Now().UTC().Truncate(time.Second)
		if last := op.Status.LastRetry; last != nil && !at.After(last.Time) {
			at = last.Add(time.Second) // always newer than the request the controller already acted on
		}

		if op.Annotations == nil {
			op.Annotations = map[string]string{}
		}

		op.Annotations[ichorv1alpha1.OperationRetryAnnotation] = at.Format(time.RFC3339)
	}

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
// a machine; the cluster lock is released by the controller (ADR 0026).
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
	// node is set for the node kind.
	node *nodePlan
	// restore is set for the etcd-restore kind.
	restore *etcdRestorePlan
	// installer is the installer image settings the Ichor upgrade asked for (ADR 0122), nil for another kind.
	installer *pb.IchorInstallerOptions
	// override is set when an incompatible target goes through under the override verb (ADR 0115):
	// the plan's warning, and the Event CreateOperation records.
	override string
	// caTargets is the ca-rotation's choice (ADR 0120), in canonical order; nil means the default.
	caTargets []string
}

// normalizeCATargets validates the CAs a ca-rotation is asked to replace and returns them in canonical
// order. A request for the default pair returns nil: the spec then carries no caRotation, as before.
func normalizeCATargets(kind string, targets []string) ([]string, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	if kind != ichorv1alpha1.TalosOperationKindCARotation {
		return nil, invalid("ca_targets only applies to a ca-rotation, not to a %s", kind)
	}

	var out []string

	for _, name := range []string{
		ichorv1alpha1.CARotationTargetTalos, ichorv1alpha1.CARotationTargetKubernetes,
		ichorv1alpha1.CARotationTargetEtcd, ichorv1alpha1.CARotationTargetAggregator,
	} {
		if slices.Contains(targets, name) {
			out = append(out, name)
		}
	}

	for _, t := range targets {
		if !slices.Contains(out, t) {
			return nil, invalid("%q is not a CA a rotation can replace; the choices are talos, kubernetes, etcd, kubernetes-aggregator", t)
		}
	}

	if len(out) != len(targets) {
		return nil, invalid("ca_targets names a CA twice")
	}

	if slices.Equal(out, ichorv1alpha1.DefaultCARotationTargets()) {
		return nil, nil
	}

	return out, nil
}

// caRotationWarning is what the user must know before an etcd or aggregator CA is rotated (ADR 0120).
func caRotationWarning(targets []string) string {
	var parts []string

	if slices.Contains(targets, ichorv1alpha1.CARotationTargetEtcd) {
		parts = append(parts, "The etcd of every control plane is restarted once in each of the three stages, one member at a time and only "+
			"while etcd is healthy; with one control plane the Kubernetes API is unavailable while its etcd restarts. "+
			"Take an etcd snapshot first (ichor etcd backup now).")
	}

	if slices.Contains(targets, ichorv1alpha1.CARotationTargetAggregator) {
		parts = append(parts, "Extension API servers (for example metrics-server) must load the new aggregator CA before the "+
			"front-proxy certificate changes; Ichor waits two minutes after kube-apiserver publishes it and restarts none of them. "+
			"One that reads the CA only once needs a restart by its owner.")
	}

	return strings.Join(parts, " ")
}

// normalizeTarget validates target for kind and returns it in the form the spec stores.
func normalizeTarget(kind, target string) (string, error) {
	switch kind {
	case ichorv1alpha1.TalosOperationKindShutdown, ichorv1alpha1.TalosOperationKindCARotation, ichorv1alpha1.TalosOperationKindConfig,
		ichorv1alpha1.TalosOperationKindNode, ichorv1alpha1.TalosOperationKindEtcdRestore:
		if target != "" {
			return "", invalid("a %s has no target", kind)
		}
	case ichorv1alpha1.TalosOperationKindCNI:
		if target != "" && target != kubeProxyEnabled && target != kubeProxyReplaced {
			return "", invalid("kube-proxy mode %q is not %q or %q", target, kubeProxyEnabled, kubeProxyReplaced)
		}
	default:
		if target == "" {
			return "", nil
		}

		v := "v" + strings.TrimPrefix(target, "v")
		if err := version.CheckSemver(v); err != nil {
			return "", connect.NewError(connect.CodeInvalidArgument, err)
		}

		return v, nil
	}

	return target, nil
}

//nolint:gocyclo // one switch over the seven kinds, each a few lines
func (s *Server) planOperation(
	ctx context.Context, clusterRef *pb.ObjectRef, pbKind pb.OperationKind, rawTarget string, maxUnavailable int32,
	node *pb.NodeActionRequest, restore *pb.EtcdRestoreRequest, bg *pb.Override, installer *pb.IchorInstallerOptions,
	rawCATargets []string,
) (*opPlan, error) {
	kind, err := kindToSpec(pbKind)
	if err != nil {
		return nil, err
	}

	caTargets, err := normalizeCATargets(kind, rawCATargets)
	if err != nil {
		return nil, err
	}

	if kind == ichorv1alpha1.TalosOperationKindNode {
		return s.planNode(ctx, node)
	}

	if kind == ichorv1alpha1.TalosOperationKindEtcdRestore {
		return s.planEtcdRestore(ctx, clusterRef, restore)
	}

	target, err := normalizeTarget(kind, rawTarget)
	if err != nil {
		return nil, err
	}

	if maxUnavailable < 0 {
		return nil, invalid("max_unavailable cannot be negative")
	}

	// A CA rotation visits the machines one at a time, control planes first (ADR 0063).
	if maxUnavailable == 0 || kind == ichorv1alpha1.TalosOperationKindCARotation {
		maxUnavailable = 1
	}

	cluster, err := s.loadCluster(ctx, namespacedName(clusterRef))
	if err != nil {
		return nil, err
	}

	if kind == ichorv1alpha1.TalosOperationKindCNI && cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(ichorv1alpha1.CNINotManagedMessage))
	}

	p := &opPlan{kind: kind, cluster: cluster, caTargets: caTargets, resp: &pb.PlanOperationResponse{MaxUnavailable: maxUnavailable}}

	if kind == ichorv1alpha1.TalosOperationKindIchor {
		if err := checkInstallerOptions(installer); err != nil {
			return nil, err
		}

		p.installer = installer
	} else if installer != nil {
		return nil, invalid("installer settings belong to an Ichor upgrade, not a %s", kind)
	}

	switch kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		p.resp.Effect, p.resp.Current = pb.OperationEffect_OPERATION_EFFECT_REBOOT, cluster.Spec.TalosVersion
	case ichorv1alpha1.TalosOperationKindKubernetes:
		p.resp.Effect, p.resp.Current = pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS, cluster.Spec.KubernetesVersion
		// The executor's four stages (ADR 0087): nothing is newer than any API server at any step.
		p.resp.Stages = []string{
			"kube-apiserver on every control plane",
			"kube-controller-manager and kube-scheduler on every control plane",
			"kube-proxy and the bootstrap manifests",
			"kubelets",
		}
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
		p.resp.Warning = caRotationWarning(p.caTargets)
		p.resp.CaTargets = p.caTargets

		if p.resp.CaTargets == nil {
			p.resp.CaTargets = ichorv1alpha1.DefaultCARotationTargets()
		}
	case ichorv1alpha1.TalosOperationKindIchor:
		p.resp.Effect = pb.OperationEffect_OPERATION_EFFECT_NONE
		p.install = &ichorv1alpha1.IchorInstallation{}

		if err := s.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, p.install); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(
					"this Ichor has no IchorInstallation, so it was installed with Helm (ADR 0104); upgrade it with `helm upgrade`, not with an Ichor operation"))
			}

			return nil, mapGetError("IchorInstallation \""+ichorv1alpha1.IchorInstallationName+"\"", err)
		}

		if p.install.HelmManaged() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(ichorv1alpha1.IchorHelmManagedMessage))
		}

		// Current is what runs, not what was asked for: after a rolled-back upgrade spec.version
		// is the failed target while the controller still reports the old version.
		p.resp.Current = p.install.Status.InstalledVersion
		if p.resp.Current == "" {
			p.resp.Current = p.install.Spec.Version
		}

	}

	specVersion := p.resp.Current
	if p.install != nil {
		specVersion = p.install.Spec.Version
	}

	// A newer Talos or Kubernetes release must be a valid step (ADR 0115). A target that is not newer
	// is left to the no-op and downgrade rules below.
	if err := checkUpgradeTarget(kind, p.resp.GetCurrent(), target, cluster.Spec.TalosVersion); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	override, err := s.checkCompatibility(ctx, cluster, kind, p.resp.GetCurrent(), target, bg)
	if err != nil {
		return nil, err
	}

	p.override = override

	p.resp.Target = target
	p.resp.SpecChanges = target != "" && target != specVersion

	if p.install != nil {
		if installerOptionsChange(p.install, p.installer) {
			p.resp.SpecChanges = true
		}

		if reason := p.installer.GetSkipSignatureReason(); reason != "" {
			p.resp.Warning = strings.TrimSpace(p.resp.GetWarning() + " " + installerSkipWarning(reason))
		}
	}

	if err := s.planMachines(ctx, p, cluster, maxUnavailable); err != nil {
		return nil, err
	}

	p.resp.Blocked, err = s.blockedReason(ctx, p, cluster)
	if err != nil {
		return nil, err
	}

	if p.override != "" {
		p.resp.Warning = strings.TrimSpace(p.resp.GetWarning() + " " + p.override)
	}

	return p, nil
}

// checkCompatibility applies the compatibility table (ADR 0115) to a Talos or Kubernetes target
// that moves the cluster. It returns "" when the step is compatible or does not move anything. An
// incompatible step is refused, unless the caller holds the override verb (ADR 0110): then the
// reason is returned for the plan's warning and the Event that CreateOperation records.
func (s *Server) checkCompatibility(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, kind, current, target string, bg *pb.Override,
) (string, error) {
	if target == "" || target == current {
		return "", nil
	}

	var err error

	switch kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		err = release.CheckTalosStep(target, cluster.Spec.KubernetesVersion)
	case ichorv1alpha1.TalosOperationKindKubernetes:
		err = release.CheckKubernetesStep(cluster.Spec.TalosVersion, target, installedCilium(cluster))
	default:
		return "", nil
	}

	if err == nil {
		return "", nil
	}

	// A version nothing knows cannot be operated, so no override makes it one (ADR 0115).
	if errors.Is(err, release.ErrUnknownVersion) {
		return "", connect.NewError(connect.CodeInvalidArgument, err)
	}

	if bg == nil {
		return "", overridableRefusal(fmt.Errorf("%w; the override verb can lift this", err))
	}

	if err := s.authorizeOverride(ctx, cluster); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s %s is incompatible (%v), overridden under override: %s", kind, target, err,
		overrideReason(bg.GetReason())), nil
}

// installedCilium is the Cilium release the status shows, "" when it is not observed.
func installedCilium(cluster *ichorv1alpha1.TalosCluster) string {
	if cluster.Status.CNI == nil {
		return ""
	}

	return release.CiliumRelease(cluster.Status.CNI.Detected, cluster.Status.CNI.DetectedVersion)
}

// notePendingSchematic records the first schematic change a Talos upgrade would make.
func notePendingSchematic(p *opPlan, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine) {
	running := m.Status.Schematic
	if running == "" {
		running = upgrade.DefaultSchematic // a node that booted no Image Factory asset runs the stock one
	}

	if want := targetSchematic(cluster); running != want && p.resp.CurrentSchematic == "" {
		p.resp.CurrentSchematic, p.resp.TargetSchematic = running, want
	}
}

// splitOwnMachine takes the machine named name (the one the controller runs on) out of both lists.
func splitOwnMachine(
	cps, workers []*ichorv1alpha1.TalosMachine, name string,
) (outCPs, outWorkers []*ichorv1alpha1.TalosMachine, own *ichorv1alpha1.TalosMachine) {
	isOwn := func(m *ichorv1alpha1.TalosMachine) bool {
		if m.Name != name {
			return false
		}

		own = m

		return true
	}

	return slices.DeleteFunc(cps, isOwn), slices.DeleteFunc(workers, isOwn), own
}

// configEffect is REBOOT when any selected machine needs a reboot and RESTART_COMPONENTS (the API
// server restart, no reboot) when none does (ADR 0080 amendment).
func configEffect(machines []*ichorv1alpha1.TalosMachine) pb.OperationEffect {
	for _, m := range machines {
		if len(m.Status.RebootPaths) > 0 {
			return pb.OperationEffect_OPERATION_EFFECT_REBOOT
		}
	}

	return pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS
}

// apiGoesDown reports whether the plan's own step takes a lone control plane's API server away:
// a reboot, or a config change (which reboots or restarts it), or an etcd CA rotation, which restarts
// the etcd member the API server of that control plane depends on (ADR 0120).
func apiGoesDown(p *opPlan) bool {
	return p.resp.Effect == pb.OperationEffect_OPERATION_EFFECT_REBOOT || p.kind == ichorv1alpha1.TalosOperationKindConfig ||
		(p.kind == ichorv1alpha1.TalosOperationKindCARotation && slices.Contains(p.caTargets, ichorv1alpha1.CARotationTargetEtcd))
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
		// (TalosMachine.status.rebootPaths, ADR 0080) or an API server restart
		// (status.apiServerRestartPaths, ADR 0080 amendment).
		if p.kind == ichorv1alpha1.TalosOperationKindConfig && !upgrade.ConfigPending(m) {
			continue
		}

		// A hold excludes a machine from a rollout, not from stopping the cluster (ADR 0067) and not from
		// a CA rotation, which would lock a machine left out when the old CA is dropped (ADR 0063).
		if p.kind != ichorv1alpha1.TalosOperationKindShutdown && p.kind != ichorv1alpha1.TalosOperationKindCARotation && m.Annotations[ichorv1alpha1.TalosMachineUpgradeHoldAnnotation] != "" {
			p.resp.Held = append(p.resp.Held, m.Name)

			continue
		}

		if p.kind == ichorv1alpha1.TalosOperationKindTalos {
			notePendingSchematic(p, cluster, m)
		}

		if m.Spec.Role == opRoleControlPlane {
			cps = append(cps, m)
		} else {
			workers = append(workers, m)
		}
	}

	slices.Sort(p.resp.Held)

	// A config rollout whose machines all need only the API server restart (no reboot) says so
	// instead of "reboot": restarts the API server on each control plane, one at a time.
	if p.kind == ichorv1alpha1.TalosOperationKindConfig {
		p.resp.Effect = configEffect(slices.Concat(cps, workers))
	}

	// With one approved control plane, rebooting it (or, for a config change, restarting its API
	// server) takes the Kubernetes API down for the length of it (the cluster has no other API
	// server to answer).
	p.resp.KubernetesApiUnavailable = apiGoesDown(p) && approvedControlPlanes == 1 && len(cps) == 1

	// Through a single node's address, every kubelet and controller talks to one API server; while
	// that node's API server restarts the whole cluster has none (ADR 0087).
	if p.kind == ichorv1alpha1.TalosOperationKindKubernetes && approvedControlPlanes > 1 &&
		cluster.Spec.Endpoint.Type == "node" && !samePatchLine(p.resp.GetCurrent(), p.resp.GetTarget()) {
		p.resp.Warning = "The cluster endpoint is the address of a single control plane (type node), so while that node's " +
			"API server is replaced the Kubernetes API is unreachable for every client, although the other control planes " +
			"are up. Use a virtual IP or a load balancer as the endpoint first."
	}

	add := func(m *ichorv1alpha1.TalosMachine, step int32) {
		pm := &pb.PlannedMachine{Name: m.Name, Role: m.Spec.Role, Step: step}
		if p.kind == ichorv1alpha1.TalosOperationKindConfig {
			pm.RebootFields = slices.Clone(m.Status.RebootPaths)
		}

		p.resp.Machines = append(p.resp.Machines, pm)
	}

	if p.kind == ichorv1alpha1.TalosOperationKindShutdown {
		// Workers together first, then every control plane in one burst (ADR 0067), each in the executor's order.
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

	// A config rollout reboots the machine the controller runs on last (ADR 0080): the controller applies
	// the same order, so the plan must list it.
	var own *ichorv1alpha1.TalosMachine

	if p.kind == ichorv1alpha1.TalosOperationKindConfig {
		cps, workers, own = splitOwnMachine(cps, workers, s.controllerMachine(ctx, list.Items, cluster.Name, ""))
	}

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

	if own != nil {
		add(own, step+1) // alone in its step, after everything else
	}

	return nil
}

func (s *Server) blockedReason(
	ctx context.Context, p *opPlan, cluster *ichorv1alpha1.TalosCluster,
) (string, error) {
	if blocked, err := s.unfinishedOperation(ctx, cluster); err != nil || blocked != "" {
		return blocked, err
	}

	if isDowngrade(p) {
		return fmt.Sprintf("cannot downgrade %s from %s to %s: the executor refuses it, and the spec is not written", p.kind, p.resp.Current, p.resp.Target), nil
	}

	if p.kind == ichorv1alpha1.TalosOperationKindCARotation && slices.Contains(p.caTargets, ichorv1alpha1.CARotationTargetAggregator) {
		contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
		if err == nil && !contract.MultidocKubernetesConfigSupported() {
			return fmt.Sprintf("the aggregator CA has an accepted list only from version contract v1.14, and the cluster has %s: "+
				"there is no moment with both CAs trusted, so it is not rotated", cluster.Spec.VersionContract), nil
		}
	}

	if p.kind == ichorv1alpha1.TalosOperationKindConfig && len(p.resp.Machines) == 0 {
		return "no approved machine has a pending configuration change that needs a reboot or an API server restart (nothing is waiting in Ready=RebootRequired or APIServerRestartRequired)", nil
	}

	return "", nil
}

// unfinishedOperation names the operation of the cluster that still runs, which blocks a new one.
// A node shutdown that is resting in ShutDown holds nothing: the node stays off by design.
func (s *Server) unfinishedOperation(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (string, error) {
	var ops ichorv1alpha1.TalosOperationList
	if err := s.List(ctx, &ops, client.InNamespace(cluster.Namespace)); err != nil {
		return "", mapListError("operations", err)
	}

	for i := range ops.Items {
		o := &ops.Items[i]
		if o.Spec.ClusterRef.Name != cluster.Name || o.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
			continue
		}

		if o.Spec.Kind == ichorv1alpha1.TalosOperationKindNode && o.Status.Phase == ichorv1alpha1.TalosOperationPhaseShutDown {
			continue
		}

		if o.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && o.Spec.Kind == ichorv1alpha1.TalosOperationKindIchor && o.Status.FailureReason != "" {
			// The installer Job failed and rolled back; a resume would only meet the same failed Job
			// (it is kept for a day), and spec.version still names the failed target.
			return fmt.Sprintf("the Ichor upgrade %q failed: the installer image was refused before it ran, or the installer rolled back "+
				"(see 'ichor operation get %s' and the installer Job's log); "+
				"delete it with 'ichor operation delete %s', then run 'ichor upgrade ichor <version>' again",
				o.Name, o.Name, o.Name), nil
		}

		if o.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused {
			return fmt.Sprintf("operation %q (%s) on this cluster is not finished (phase Paused); "+
				"resume it with 'ichor operation resume %s', or delete it with 'ichor operation delete %s'",
				o.Name, o.Spec.Kind, o.Name, o.Name), nil
		}

		return fmt.Sprintf("operation %q (%s) on this cluster is not finished (phase %q); wait for it, or delete it if it is stuck",
			o.Name, o.Spec.Kind, o.Status.Phase), nil
	}

	return "", nil
}

// PlanOperation reports what CreateOperation would do, without writing.
func (s *Server) PlanOperation(
	ctx context.Context, req *connect.Request[pb.PlanOperationRequest],
) (*connect.Response[pb.PlanOperationResponse], error) {
	p, err := s.planOperation(ctx, req.Msg.GetCluster(), req.Msg.GetKind(), req.Msg.GetTarget(), req.Msg.GetMaxUnavailable(), req.Msg.GetNode(), req.Msg.GetEtcdRestore(), req.Msg.GetOverride(), req.Msg.GetIchorInstaller(), req.Msg.GetCaTargets())
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
// create operations leaves its spec alone (docs/adr/0068).
func (s *Server) CreateOperation(
	ctx context.Context, req *connect.Request[pb.CreateOperationRequest],
) (*connect.Response[pb.CreateOperationResponse], error) {
	m := req.Msg

	p, err := s.planOperation(ctx, m.GetCluster(), m.GetKind(), m.GetTarget(), m.GetMaxUnavailable(), m.GetNode(), m.GetEtcdRestore(), m.GetOverride(), m.GetIchorInstaller(), m.GetCaTargets())
	if err != nil {
		return nil, err
	}

	if p.resp.GetBlocked() != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(p.resp.GetBlocked()))
	}

	if p.node != nil {
		resp, err := s.createNodeOperation(ctx, p)
		if err != nil {
			return nil, err
		}

		return connect.NewResponse(resp), nil
	}

	if p.restore != nil {
		resp, err := s.createEtcdRestore(ctx, p)
		if err != nil {
			return nil, err
		}

		return connect.NewResponse(resp), nil
	}

	cluster := p.cluster
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: p.kind + "-", Namespace: cluster.Namespace, Annotations: s.startedByAnnotation(ctx),
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: p.kind, MaxUnavailable: m.GetMaxUnavailable(),
		},
	}
	if len(p.caTargets) > 0 {
		op.Spec.CARotation = &ichorv1alpha1.TalosOperationCARotationSpec{Targets: p.caTargets}
	}

	clusterKey := client.ObjectKeyFromObject(cluster)

	if p.resp.GetSpecChanges() && p.resp.GetCurrent() != "" && slices.Contains([]string{
		ichorv1alpha1.TalosOperationKindTalos, ichorv1alpha1.TalosOperationKindKubernetes, ichorv1alpha1.TalosOperationKindIchor,
	}, p.kind) {
		op.Annotations[ichorv1alpha1.OperationFromVersionAnnotation] = p.resp.GetCurrent()
	}

	if err := s.authorizeStart(ctx, &op.Spec, cluster.Namespace); err != nil {
		return nil, err
	}

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

	// An incompatible step that went through under the override verb is recorded in the cluster's
	// Events (ADR 0110, ADR 0115).
	if p.override != "" {
		if err := s.emitWarningEvent(ctx, cluster, kindTalosCluster, "Override", p.override); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if p.kind == ichorv1alpha1.TalosOperationKindCNI {
		if err := s.clearKubeProxyConsent(ctx, cluster); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	return connect.NewResponse(&pb.CreateOperationResponse{
		Operation: operationToPB(op), Audit: s.opAudit(ctx, "create", objectRef(op.Namespace, op.Name)), SpecChanged: p.resp.GetSpecChanges(),
	}), nil
}

// writeTarget stores the target in the spec it belongs to.
func (s *Server) writeTarget(ctx context.Context, p *opPlan) error {
	target := p.resp.GetTarget()
	key := client.ObjectKeyFromObject(p.cluster)

	var mutate func(*ichorv1alpha1.TalosCluster)

	switch p.kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		mutate = func(c *ichorv1alpha1.TalosCluster) { c.Spec.TalosVersion = target }
	case ichorv1alpha1.TalosOperationKindKubernetes:
		mutate = func(c *ichorv1alpha1.TalosCluster) { c.Spec.KubernetesVersion = target }
	case ichorv1alpha1.TalosOperationKindIchor:
		if target != "" {
			p.install.Spec.Version = target
		}

		applyInstallerOptions(&p.install.Spec, p.installer)

		if err := s.Update(ctx, p.install, strictFieldValidation); err != nil {
			return mapOperationWriteError("updating IchorInstallation", client.ObjectKeyFromObject(p.install), err)
		}

		return nil
	case ichorv1alpha1.TalosOperationKindCNI:
		// ADR 0013's admission policy denies a kubeProxy change without this
		// annotation. It is set for the one write that changes the mode and
		// removed again once the operation exists (clearKubeProxyConsent), so it never
		// lingers as a standing consent for someone else's later edit.
		mutate = func(c *ichorv1alpha1.TalosCluster) {
			if c.Annotations == nil {
				c.Annotations = map[string]string{}
			}

			c.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation] = target
			c.Spec.CNI.KubeProxy = target
		}
	}

	if err := s.updateCluster(ctx, p.cluster, mutate); err != nil {
		return mapOperationWriteError("updating cluster", key, err)
	}

	return nil
}

// updateCluster applies mutate to the cluster and updates it, retrying with a fresh read when the
// controller (or anyone) wrote it in between: the controller writes the cluster's status and
// metadata often, and a stale resourceVersion must not strand a half-made switch. cluster holds
// the stored object afterwards.
func (s *Server) updateCluster(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, mutate func(*ichorv1alpha1.TalosCluster)) error {
	key := client.ObjectKeyFromObject(cluster)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh ichorv1alpha1.TalosCluster
		if err := s.Get(ctx, key, &fresh); err != nil {
			return err
		}

		mutate(&fresh)

		if err := s.Update(ctx, &fresh, strictFieldValidation); err != nil {
			return err
		}

		fresh.DeepCopyInto(cluster)

		return nil
	})
}

// clearKubeProxyConsent removes the kube-proxy confirmation annotation once the operation exists.
// It runs after the operation is created, so a failure here can never leave a changed mode with no
// operation, and for every cni start, also one whose spec was already changed (a retry after an
// earlier failure, or a Git-first change that carried the annotation): a consent left on the
// cluster would admit a later hand edit of the mode. The removal is not worth failing a created
// operation for; a leftover is reported to the caller as an error naming it.
func (s *Server) clearKubeProxyConsent(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) error {
	if _, ok := cluster.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation]; !ok {
		return nil
	}

	err := s.updateCluster(ctx, cluster, func(c *ichorv1alpha1.TalosCluster) {
		delete(c.Annotations, ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation)
	})
	if err != nil {
		return fmt.Errorf("the operation was created but the annotation %s could not be removed from cluster %q: %w",
			ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation, cluster.Name, err)
	}

	return nil
}

// checkUpgradeTarget applies the upgrade target rule (ADR 0115) to a Talos or Kubernetes target that
// is newer than what runs: the Talos path, or the Kubernetes skew and pairing. The rule is the
// server's plan and the CLI's, and the executors enforce it again. Other kinds, empty targets and
// targets that are not newer are not checked here.
func checkUpgradeTarget(kind, current, target, talos string) error {
	if target == "" || !semver.IsValid(current) || semver.Compare(target, current) <= 0 {
		return nil
	}

	switch kind {
	case ichorv1alpha1.TalosOperationKindTalos:
		return release.CheckTalosTarget(current, target)
	case ichorv1alpha1.TalosOperationKindKubernetes:
		return release.CheckKubernetesTarget(current, target, talos)
	}

	return nil
}

// isDowngrade reports a version target older than the current one for the
// kinds that carry a version (talos, kubernetes, ichor). The executors refuse
// it too, but only after the CLI has written the lower version into the spec.
// samePatchLine reports that moving from current to target stays within one minor version. An
// empty or unparsable target (the spec already says it) is not known to, so it is not "same".
func samePatchLine(current, target string) bool {
	return semver.IsValid(current) && semver.IsValid(target) && semver.MajorMinor(current) == semver.MajorMinor(target)
}

func isDowngrade(p *opPlan) bool {
	switch p.kind {
	case ichorv1alpha1.TalosOperationKindTalos, ichorv1alpha1.TalosOperationKindKubernetes, ichorv1alpha1.TalosOperationKindIchor:
	default:
		return false
	}

	t, c := p.resp.GetTarget(), p.resp.GetCurrent()

	return semver.IsValid(t) && semver.IsValid(c) && semver.Compare(t, c) < 0
}

// targetSchematic is the schematic a cluster's nodes should run: spec.schematic.id, or the stock one.
func targetSchematic(cluster *ichorv1alpha1.TalosCluster) string {
	if cluster.Spec.Schematic != nil && cluster.Spec.Schematic.ID != "" {
		return cluster.Spec.Schematic.ID
	}

	return upgrade.DefaultSchematic
}
