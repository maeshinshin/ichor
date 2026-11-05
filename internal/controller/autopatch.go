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

package controller

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/autopatch"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// autoPatchInterval is the time between two passes. The window is a time of day, so the loop runs far
// more often than the release check (every 6 hours): the check decides what is on offer, this decides when.
const autoPatchInterval = time.Minute

// autoPatchMarker is the value of the annotation that marks an operation as the patcher's.
const autoPatchMarker = "true"

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch

// AutoPatcher starts upgrades to patch releases of the running minor when the setting allows it.
// It runs on the leader only, once a minute. It reads the candidates the release check recorded in
// IchorInstallation.status.upgradeTargets, decides with package autopatch, and starts at most one
// operation per cluster by creating a TalosOperation, which the normal executor then runs. A Talos or
// Kubernetes upgrade also needs the version in the cluster's spec. The controller never writes that spec:
// the version and the operation are written by Actor, a client that impersonates the ServiceAccount
// ichor-auto-patch, a delegated identity that holds only get and update on talosclusters and create on
// talosoperations, and that the admission webhook limits to a newer patch of the running minor. The audit
// log shows the writes as that user. A failed or paused automatic operation halts the patcher until a person clears it; it
// never starts the operation again by itself.
type AutoPatcher struct {
	// Client writes the IchorInstallation status and nothing else.
	Client client.Client
	// Actor writes the TalosCluster version and creates the operation, as the ServiceAccount ichor-auto-patch. Never
	// the controller's own identity.
	Actor client.Client
	// Reader reads without the cache: a start must see the operation it made a minute ago.
	Reader client.Reader
	// Settings resolves spec.autoPatch against the chart's values.
	Settings *settings.Reader
	// Recorder emits the Events.
	Recorder record.EventRecorder
	// Interval is the time between passes; zero means autoPatchInterval.
	Interval time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Namespace limits the patcher to the clusters of one namespace; empty means all (tests set it).
	Namespace string
}

// NeedLeaderElection makes the patcher run on the leader only.
func (p *AutoPatcher) NeedLeaderElection() bool { return true }

// Start runs a pass now and then on the interval, until ctx ends.
func (p *AutoPatcher) Start(ctx context.Context) error {
	interval := cmp.Or(p.Interval, autoPatchInterval)

	wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
		if err := p.Pass(ctx); err != nil {
			logf.FromContext(ctx).WithName("autopatch").Info("An automatic patch pass failed", "error", err.Error())
		}
	}, interval, 0.1, true)

	return nil
}

func (p *AutoPatcher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}

	return time.Now()
}

func isAutoOperation(op *ichorv1alpha1.TalosOperation) bool {
	return op.Annotations[ichorv1alpha1.OperationAutoPatchAnnotation] == autoPatchMarker
}

func opRef(op *ichorv1alpha1.TalosOperation) string { return op.Namespace + "/" + op.Name }

// isUpgradeKind is the kinds a person's completed operation may end a halt with.
func isUpgradeKind(kind string) bool {
	return kind == ichorv1alpha1.TalosOperationKindTalos || kind == ichorv1alpha1.TalosOperationKindKubernetes ||
		kind == ichorv1alpha1.TalosOperationKindCNI
}

// unfinished reports an operation that still holds the cluster: not Completed, except a node shutdown that
// rests in ShutDown by design.
func unfinished(op *ichorv1alpha1.TalosOperation) bool {
	if op.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted {
		return false
	}

	return op.Spec.Kind != ichorv1alpha1.TalosOperationKindNode || op.Status.Phase != ichorv1alpha1.TalosOperationPhaseShutDown
}

// Pass runs one pass: keep the halt state right, then, when the setting is on and nothing is halted, start
// what the decision says for each cluster.
func (p *AutoPatcher) Pass(ctx context.Context) error {
	eff, err := p.Settings.Effective(ctx)
	if err != nil {
		return fmt.Errorf("reading the settings: %w", err)
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := p.Reader.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		return client.IgnoreNotFound(err)
	}

	halted := ""
	if inst.Status.AutoPatch != nil {
		halted = inst.Status.AutoPatch.HaltedOperation
	}

	if !eff.AutoPatch.V.Enabled && halted == "" {
		return nil
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := p.Reader.List(ctx, &ops); err != nil {
		return fmt.Errorf("listing the operations: %w", err)
	}

	if halted, err = p.keepHalt(ctx, &inst, halted, ops.Items); err != nil {
		return err
	}

	if !eff.AutoPatch.V.Enabled || halted != "" {
		return nil
	}

	var clusters ichorv1alpha1.TalosClusterList
	if err := p.Reader.List(ctx, &clusters, client.InNamespace(p.Namespace)); err != nil {
		return fmt.Errorf("listing the clusters: %w", err)
	}

	ichorUpgrading := inst.Status.InstalledVersion != "" && inst.Status.InstalledVersion != inst.Spec.Version

	for i := range clusters.Items {
		if err := p.passCluster(ctx, &inst, &clusters.Items[i], ops.Items, eff.AutoPatch.V, ichorUpgrading); err != nil {
			logf.FromContext(ctx).WithName("autopatch").Info("An automatic patch start failed",
				"cluster", clusters.Items[i].Name, "error", err.Error())
		}
	}

	return nil
}

// keepHalt ends a halt that is over and starts one for an automatic operation that failed or paused. It
// returns the halted operation after the pass ("" when not halted).
func (p *AutoPatcher) keepHalt(
	ctx context.Context, inst *ichorv1alpha1.IchorInstallation, halted string, ops []ichorv1alpha1.TalosOperation,
) (string, error) {
	if halted != "" {
		var halting *autopatch.Halting

		var later []autopatch.LaterOperation

		for i := range ops {
			o := &ops[i]
			if opRef(o) == halted {
				halting = &autopatch.Halting{
					Cluster: o.Spec.ClusterRef.Name, Created: o.CreationTimestamp.Time,
					Completed: o.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted,
				}
			}

			if isUpgradeKind(o.Spec.Kind) {
				later = append(later, autopatch.LaterOperation{
					Cluster: o.Spec.ClusterRef.Name, Created: o.CreationTimestamp.Time, Auto: isAutoOperation(o),
					Completed: o.Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted,
				})
			}
		}

		ack := ""
		if a := inst.Spec.AutoPatch; a != nil {
			ack = a.AcknowledgedHalt
		}

		if !autopatch.Resume(halted, ack, halting, later) {
			return halted, nil
		}

		if err := p.setHalt(ctx, inst, "", ""); err != nil {
			return halted, err
		}

		p.Recorder.Eventf(inst, corev1.EventTypeNormal, "AutoPatchResumed", "Automatic patching resumed after %s", halted)

		return "", nil
	}

	for i := range ops {
		o := &ops[i]
		if !isAutoOperation(o) || o.Status.Phase != ichorv1alpha1.TalosOperationPhasePaused {
			continue
		}

		reason := cmp.Or(o.Status.FailureReason, "the operation is paused")
		if err := p.setHalt(ctx, inst, opRef(o), fmt.Sprintf("automatic operation %s (%s) is paused: %s", opRef(o), o.Spec.Kind, reason)); err != nil {
			return "", err
		}

		p.Recorder.Eventf(inst, corev1.EventTypeWarning, "AutoPatchHalted",
			"Automatic patching stopped: operation %s (%s) is paused: %s. Resume or delete it, or acknowledge it with 'ichor settings set %s=%s'",
			opRef(o), o.Spec.Kind, reason, settings.KeyAutoPatchAckHalt, opRef(o))

		return opRef(o), nil
	}

	return "", nil
}

// setHalt records the halt (operation namespace/name and message) or, with empty arguments, clears it.
func (p *AutoPatcher) setHalt(ctx context.Context, inst *ichorv1alpha1.IchorInstallation, operation, message string) error {
	return p.patchStatus(ctx, inst, func(s *ichorv1alpha1.IchorInstallationStatus) {
		if s.AutoPatch == nil {
			s.AutoPatch = &ichorv1alpha1.IchorAutoPatchStatus{}
		}

		s.AutoPatch.HaltedOperation = operation

		if operation == "" {
			apimeta.RemoveStatusCondition(&s.Conditions, ichorv1alpha1.ConditionAutoPatchHalted)

			return
		}

		apimeta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionAutoPatchHalted, Status: metav1.ConditionTrue, Reason: "OperationNotFinished", Message: message,
		})
	})
}

func (p *AutoPatcher) patchStatus(ctx context.Context, inst *ichorv1alpha1.IchorInstallation, mutate func(*ichorv1alpha1.IchorInstallationStatus)) error {
	orig := inst.DeepCopy()

	mutate(&inst.Status)

	return p.Client.Status().Patch(ctx, inst, client.MergeFrom(orig))
}

// passCluster decides for one cluster and starts what the decision says.
func (p *AutoPatcher) passCluster(
	ctx context.Context, inst *ichorv1alpha1.IchorInstallation, cluster *ichorv1alpha1.TalosCluster,
	ops []ichorv1alpha1.TalosOperation, setting settings.AutoPatch, ichorUpgrading bool,
) error {
	in := autopatch.Input{
		Now: p.now(), Setting: setting, Enabled: true, IchorUpgrading: ichorUpgrading, Targets: inst.Status.UpgradeTargets,
		Cluster: autopatch.Cluster{
			Talos: cluster.Spec.TalosVersion, Kubernetes: cluster.Spec.KubernetesVersion,
			CNIUnmanaged: cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged,
		},
	}

	if c := cluster.Status.CNI; c != nil {
		in.Cluster.CNIDetected = c.Detected
		if c.Detected == ciliumName {
			in.Cluster.CiliumVersion = strings.TrimPrefix(c.DetectedVersion, "v")
		}
	}

	for i := range ops {
		o := &ops[i]
		if o.Namespace != cluster.Namespace || o.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		if unfinished(o) && in.Busy == "" {
			in.Busy = o.Name
		}

		if isAutoOperation(o) && o.CreationTimestamp.After(in.LastStart) {
			in.LastStart = o.CreationTimestamp.Time
		}
	}

	ready, err := p.machinesReady(ctx, cluster)
	if err != nil {
		return err
	}

	in.Cluster.MachinesReady = ready

	_, in.LockHeld, err = clusterlock.New(p.Client, client.ObjectKeyFromObject(cluster), 0).WithReader(p.Reader).Holder(ctx)
	if err != nil {
		return fmt.Errorf("reading the cluster lock: %w", err)
	}

	d := autopatch.Decide(in)
	if !d.Start() {
		return nil
	}

	return p.start(ctx, inst, cluster, d)
}

// machinesReady is true when the cluster has approved machines and each is Ready and not being deleted.
func (p *AutoPatcher) machinesReady(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (bool, error) {
	machines, err := deleteguard.ApprovedMachines(ctx, p.Reader, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{IncludeDeleting: true})
	if err != nil {
		return false, fmt.Errorf("listing the machines: %w", err)
	}

	for i := range machines {
		if !machines[i].DeletionTimestamp.IsZero() || !apimeta.IsStatusConditionTrue(machines[i].Status.Conditions, conditionReady) {
			return false, nil
		}
	}

	return len(machines) > 0, nil
}

// start creates the operation of the decision. For Talos and Kubernetes the version goes into the cluster's
// spec first, as the UI does (the executors read the target from it), after a dry run of the operation's
// creation so that nothing is written when the operation could not be created.
func (p *AutoPatcher) start(ctx context.Context, inst *ichorv1alpha1.IchorInstallation, cluster *ichorv1alpha1.TalosCluster, d autopatch.Decision) error {
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: autoPatchKind(d.Component) + "-", Namespace: cluster.Namespace,
		Annotations: map[string]string{
			ichorv1alpha1.OperationAutoPatchAnnotation: autoPatchMarker,
			ichorv1alpha1.OperationStartedByAnnotation: ichorv1alpha1.AutoPatchStarter,
		},
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: autoPatchKind(d.Component),
		},
	}

	from := versionFrom(cluster, d.Component)
	op.Annotations[ichorv1alpha1.OperationFromVersionAnnotation] = from

	if err := p.Actor.Create(ctx, op.DeepCopy(), client.DryRunAll); err != nil {
		return fmt.Errorf("the %s operation could not be created: %w", op.Spec.Kind, err)
	}

	if err := p.writeVersion(ctx, cluster, d); err != nil {
		return fmt.Errorf("writing the %s version %s: %w", d.Component, d.Target, err)
	}

	if err := retry.OnError(retry.DefaultBackoff, func(error) bool { return true }, func() error { return p.Actor.Create(ctx, op) }); err != nil {
		// The version is written and no operation exists: say so, and stop, instead of leaving it unseen.
		ref := cluster.Namespace + "/" + cluster.Name
		msg := fmt.Sprintf("the %s version %s was written to cluster %s but its operation could not be created: %v", d.Component, d.Target, ref, err)

		if herr := p.setHalt(ctx, inst, ref, msg); herr != nil {
			return herr
		}

		p.Recorder.Eventf(inst, corev1.EventTypeWarning, "AutoPatchHalted", "Automatic patching stopped: %s", msg)

		return fmt.Errorf("%s", msg)
	}

	p.Recorder.Eventf(cluster, corev1.EventTypeNormal, "AutoPatchStarted",
		"Automatic patching started operation %s: %s %s to %s", op.Name, d.Component, from, d.Target)

	now := metav1.NewTime(p.now())

	return p.patchStatus(ctx, inst, func(s *ichorv1alpha1.IchorInstallationStatus) {
		if s.AutoPatch == nil {
			s.AutoPatch = &ichorv1alpha1.IchorAutoPatchStatus{}
		}

		s.AutoPatch.LastOperation, s.AutoPatch.LastStartedAt = opRef(op), &now
	})
}

func autoPatchKind(component string) string {
	if component == ichorv1alpha1.AutoPatchKubernetes {
		return ichorv1alpha1.TalosOperationKindKubernetes
	}

	return ichorv1alpha1.TalosOperationKindTalos
}

// writeVersion is the controller's one write to a TalosCluster spec. It reads the cluster fresh, and
// writes only when the field still holds an older patch of the target's minor, so a person's edit in between is
// never overwritten.
func (p *AutoPatcher) writeVersion(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, d autopatch.Decision) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var fresh ichorv1alpha1.TalosCluster
		if err := p.Reader.Get(ctx, client.ObjectKeyFromObject(cluster), &fresh); err != nil {
			return err
		}

		field := &fresh.Spec.TalosVersion
		if d.Component == ichorv1alpha1.AutoPatchKubernetes {
			field = &fresh.Spec.KubernetesVersion
		}

		if *field == d.Target {
			return nil
		}

		if from := versionFrom(cluster, d.Component); *field != from {
			return fmt.Errorf("the version changed from %s to %s since it was decided", from, *field)
		}

		*field = d.Target

		return p.Actor.Update(ctx, &fresh)
	})
}

func versionFrom(c *ichorv1alpha1.TalosCluster, component string) string {
	if component == ichorv1alpha1.AutoPatchKubernetes {
		return c.Spec.KubernetesVersion
	}

	return c.Spec.TalosVersion
}
