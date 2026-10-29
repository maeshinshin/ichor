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
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// defaultEvacuationTimeout is used when the matching TalosStorage's own
// spec.evacuationTimeout is unset ("Unset means the hook's own default",
// TalosStorageSpec's doc comment). No ADR fixes a number for that default;
// ten minutes is a generous, round wait for one Longhorn replica to rebuild
// elsewhere before this pauses the rollout rather than proceeding to drain.
const defaultEvacuationTimeout = 10 * time.Minute

// defaultLonghornNamespace is Longhorn's own documented default install
// namespace.
//
// ponytail: TalosStorageSpec carries no namespace field, so a Longhorn
// install in a non-default namespace cannot be evacuated by this code.
// Upgrade by adding a namespace field to TalosStorageSpec if that is ever
// needed -- nothing else here assumes "longhorn-system" beyond this one
// constant.
const defaultLonghornNamespace = "longhorn-system"

// evacuationTarget is what StorageEvacuator.Resolve found for one node:
// which storage.EvacuationHook to drive, how long to wait before pausing,
// and which namespace its storage-system resources live in.
type evacuationTarget struct {
	hook      storage.EvacuationHook
	timeout   time.Duration
	namespace string
}

// StorageEvacuator is layer 4's caller-side counterpart to pkg/storage's
// EvacuationHook/EvacuationStatus split (ADR 0025): pkg/storage makes no
// cluster call of its own, so something in internal/controller has to find
// which TalosStorage (if any) applies to a node, and read/write that
// storage system's own resources. Declared here, the consumer, the same
// way ADR 0025 itself closes ("Go's own convention is that interfaces are
// declared by the consumer, at the point of consumption") and the same way
// AdvanceMachineFunc already is (ADR 0026) -- talosUpgradeAdvancer is this
// interface's consumer.
type StorageEvacuator interface {
	// Resolve finds the TalosStorage (if any) in cluster whose node
	// selector matches node. nil, nil means none does: nothing to
	// evacuate, which is the common case for a cluster with no storage
	// system configured at all.
	Resolve(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, node string) (*evacuationTarget, error)

	// Begin writes target.hook.BeginEvacuation's two values to node's
	// storage-system resource, in the order BeginEvacuation names.
	Begin(ctx context.Context, target *evacuationTarget, node string) error

	// ReplicasRemaining counts the storage system's own replica-shaped
	// resources still naming node -- the count storage.EvacuationStatus
	// needs.
	ReplicasRemaining(ctx context.Context, target *evacuationTarget, node string) (int, error)

	// End writes target.hook.EndEvacuation's two values to node's
	// storage-system resource, in the order EndEvacuation names.
	End(ctx context.Context, target *evacuationTarget, node string) error
}

// LonghornEvacuator is the StorageEvacuator for TalosStorage.spec.diskPreset
// "longhorn-v1" (pkg/storage's only preset today, ADR 0025). It reads and
// writes Longhorn's nodes.longhorn.io/v1beta2 and replicas.longhorn.io/v1beta2
// as unstructured.Unstructured -- the same technique
// cnihandover_controller.go already uses for Argo CD's Application -- never
// as a typed dependency on longhorn-manager's own API types, which ADR 0025
// explicitly rejected ("adding longhorn-manager's own API types as a
// dependency here ... was considered and rejected").
type LonghornEvacuator struct {
	client.Client

	// noted remembers which nodes already had their "nothing to evacuate" info line logged.
	noted sync.Map
}

const longhornGroup = "longhorn.io"

var (
	longhornNodeGVK        = schema.GroupVersionKind{Group: longhornGroup, Version: "v1beta2", Kind: "Node"}
	longhornReplicaListGVK = schema.GroupVersionKind{Group: longhornGroup, Version: "v1beta2", Kind: "ReplicaList"}
)

// Longhorn's own objects, read and written in whichever namespace it is installed in. The
// verbs are the ones Begin/End/ReplicasRemaining use and no more; talosstorages is read by
// Resolve. (ADR 0050 notes these were missing from the controller's role until now.)
//
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages,verbs=get;list;watch
// +kubebuilder:rbac:groups=longhorn.io,resources=nodes,verbs=get;update
// +kubebuilder:rbac:groups=longhorn.io,resources=replicas,verbs=list

// Resolve implements StorageEvacuator. It only reads TalosStorage and the
// Kubernetes Node, neither Longhorn-specific, so (unlike Begin/
// ReplicasRemaining/End) it needs no real Longhorn CRDs to exercise in a
// test.
func (e *LonghornEvacuator) Resolve(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, node string,
) (*evacuationTarget, error) {
	var n corev1.Node
	if err := e.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return nil, fmt.Errorf("reading node %q to match it against TalosStorage: %w", node, err)
	}

	var storages ichorv1alpha1.TalosStorageList
	if err := e.List(ctx, &storages, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}

	// The one selection rule, shared with pkg/render's layer-1 injection (ADR 0050).
	chosen, err := storage.Select(cluster, storages.Items, n.Labels)
	if err != nil {
		return nil, err
	}

	if chosen == nil {
		return nil, nil
	}

	hook, err := storage.NewEvacuationHook(chosen.Spec.DiskPreset)
	if err != nil {
		return nil, err
	}

	timeout := defaultEvacuationTimeout
	if chosen.Spec.EvacuationTimeout != nil {
		timeout = chosen.Spec.EvacuationTimeout.Duration
	}

	return &evacuationTarget{hook: hook, timeout: timeout, namespace: defaultLonghornNamespace}, nil
}

// Begin implements StorageEvacuator. Scheduling is disabled first, then the eviction requested:
// Longhorn's webhook rejects a write that leaves both true.
func (e *LonghornEvacuator) Begin(ctx context.Context, target *evacuationTarget, node string) error {
	allowScheduling, evictionRequested := target.hook.BeginEvacuation()

	return e.writeNodeEvacuation(ctx, target, node, [2]nodeField{
		{fieldAllowScheduling, allowScheduling}, {fieldEvictionRequested, evictionRequested},
	})
}

// End implements StorageEvacuator. The eviction is cancelled first, then scheduling enabled: the
// webhook ("need to disable scheduling on node X for node eviction, or cancel eviction to enable
// scheduling on this node") refuses allowScheduling=true while evictionRequested is still true
// (ADR 0025, Amendment).
func (e *LonghornEvacuator) End(ctx context.Context, target *evacuationTarget, node string) error {
	allowScheduling, evictionRequested := target.hook.EndEvacuation()

	return e.writeNodeEvacuation(ctx, target, node, [2]nodeField{
		{fieldEvictionRequested, evictionRequested}, {fieldAllowScheduling, allowScheduling},
	})
}

// The two Longhorn Node spec fields the evacuation toggles.
const (
	fieldAllowScheduling   = "allowScheduling"
	fieldEvictionRequested = "evictionRequested"
)

// nodeField is one boolean of a Longhorn Node's spec.
type nodeField struct {
	name  string
	value bool
}

// absent reports whether err means Longhorn itself is not there to evacuate: its CRD is not
// installed, or it has no Node object for this machine (e.g. longhorn-manager is crash-looping
// because the schematic still lacks iscsi-tools). A TalosStorage legitimately exists before
// Longhorn does (the disk comes first), so this is "nothing to evacuate", not a failure. The
// first sighting per node is logged at info level.
func (e *LonghornEvacuator) absent(ctx context.Context, node string, err error) bool {
	if !apimeta.IsNoMatchError(err) && !apierrors.IsNotFound(err) {
		return false
	}

	if _, seen := e.noted.LoadOrStore(node, struct{}{}); !seen {
		logf.FromContext(ctx).Info("Longhorn has no Node object for this node (CRD or object absent); nothing to evacuate",
			"node", node, "reason", err.Error())
	}

	return true
}

// writeNodeEvacuation writes the two fields to node's nodes.longhorn.io object as two separate
// updates, in the order given (a field already holding its value is not rewritten). Longhorn's
// admission webhook rejects a write that leaves evictionRequested true while allowScheduling is
// also true, so the order differs for Begin and End (ADR 0025 and its Amendment). A missing CRD
// or Node object is nothing to evacuate and returns nil.
func (e *LonghornEvacuator) writeNodeEvacuation(
	ctx context.Context, target *evacuationTarget, node string, fields [2]nodeField,
) error {
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(longhornNodeGVK)

	if err := e.Get(ctx, types.NamespacedName{Namespace: target.namespace, Name: node}, &obj); err != nil {
		if e.absent(ctx, node, err) {
			return nil
		}

		return fmt.Errorf("reading Longhorn node %q: %w", node, err)
	}

	for _, f := range fields {
		if cur, found, _ := unstructured.NestedBool(obj.Object, "spec", f.name); found && cur == f.value {
			continue
		}

		if err := unstructured.SetNestedField(obj.Object, f.value, "spec", f.name); err != nil {
			return err
		}

		if err := e.Update(ctx, &obj); err != nil {
			return fmt.Errorf("writing Longhorn node %q's %s: %w", node, f.name, err)
		}
	}

	return nil
}

// ReplicasRemaining implements StorageEvacuator: it counts
// replicas.longhorn.io objects whose spec.nodeID still names node --
// longhorn-manager v1.9.1's own VolumeController.cleanupEvictionRequestedReplicas
// deletes exactly that object once a healthy replacement replica exists
// elsewhere (ADR 0025). Whole-node eviction only, never the per-disk form
// (spec.diskID): architecture.md's node-operation hook evacuates a whole
// node before an upgrade, never one disk.
func (e *LonghornEvacuator) ReplicasRemaining(ctx context.Context, target *evacuationTarget, node string) (int, error) {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(longhornReplicaListGVK)

	if err := e.List(ctx, &list, client.InNamespace(target.namespace)); err != nil {
		if e.absent(ctx, node, err) {
			return 0, nil
		}

		return 0, fmt.Errorf("listing Longhorn replicas: %w", err)
	}

	count := 0

	for _, item := range list.Items {
		nodeID, _, _ := unstructured.NestedString(item.Object, "spec", "nodeID")
		if nodeID == node {
			count++
		}
	}

	return count, nil
}

// conditionStorageEvacuation is a TalosMachine status condition, True from
// BeginEvacuation's write until EvacuationStatus reports paused (or the
// caller ends it). LastTransitionTime is evacuateMachine's own "elapsed"
// clock start. Shared by the Talos upgrade (talos_upgrade.go) and node
// removal (removal.go): both are node operations that must go through the
// same evacuation hook (architecture.md, "Storage"), so the decision lives
// here once.
const (
	conditionStorageEvacuation = "StorageEvacuation"
	reasonEvacuating           = "Evacuating"
	reasonEvacuationStopped    = "EvacuationStopped"
)

// evacuateMachine drives one storage.EvacuationStatus step for m's node:
// Begin on the first call (returning EvacuationWaiting so the caller keeps
// reconciling), then ReplicasRemaining against the elapsed time since
// conditionStorageEvacuation's own LastTransitionTime on every call after.
// On EvacuationPaused it also calls End (a paused evacuation gives up and
// re-enables scheduling). On EvacuationDone it deliberately does NOT call
// End: what follows (a drain and reboot, or a removal) decides when
// scheduling is meaningful again. purpose completes "evacuating node X
// before <purpose>" in the condition message.
//
// ev nil, no matching TalosStorage, or the Kubernetes Node already gone
// all mean there is nothing to evacuate: EvacuationDone, not an error.
func evacuateMachine(
	ctx context.Context, c client.Client, ev StorageEvacuator,
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, node, purpose string,
) (storage.EvacuationProgress, error) {
	if ev == nil {
		return storage.EvacuationDone, nil
	}

	target, err := ev.Resolve(ctx, cluster, node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return storage.EvacuationDone, nil // no Node object: no replicas to move off it
		}

		return 0, err
	}

	if target == nil {
		return storage.EvacuationDone, nil
	}

	cond := apimeta.FindStatusCondition(m.Status.Conditions, conditionStorageEvacuation)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		if err := ev.Begin(ctx, target, node); err != nil {
			return 0, err
		}

		if err := setEvacuationCondition(ctx, c, m, metav1.ConditionTrue, reasonEvacuating,
			fmt.Sprintf("evacuating node %q before %s", node, purpose)); err != nil {
			return 0, err
		}

		return storage.EvacuationWaiting, nil
	}

	elapsed := time.Since(cond.LastTransitionTime.Time)

	remaining, err := ev.ReplicasRemaining(ctx, target, node)
	if err != nil {
		return 0, err
	}

	progress := storage.EvacuationStatus(remaining, elapsed, target.timeout)
	if progress == storage.EvacuationPaused {
		if err := endMachineEvacuation(ctx, c, ev, cluster, m, node); err != nil {
			return 0, err
		}
	}

	return progress, nil
}

// endMachineEvacuation undoes a pending evacuation (re-enables scheduling),
// a no-op if conditionStorageEvacuation is not currently True -- covering
// both "evacuation was never needed for this node" and "it was already
// ended".
func endMachineEvacuation(
	ctx context.Context, c client.Client, ev StorageEvacuator,
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, node string,
) error {
	if ev == nil || !apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionStorageEvacuation) {
		return nil
	}

	target, err := ev.Resolve(ctx, cluster, node)
	if err != nil {
		return err
	}

	if target == nil {
		return nil
	}

	if err := ev.End(ctx, target, node); err != nil {
		return err
	}

	return setEvacuationCondition(ctx, c, m, metav1.ConditionFalse, reasonEvacuationStopped,
		fmt.Sprintf("scheduling re-enabled on node %q", node))
}

// endEvacuationOf is endMachineEvacuation for a machine whose node name is not yet known: it
// resolves the name (talos may be nil) only when an evacuation is pending.
func endEvacuationOf(
	ctx context.Context, c client.Client, ev StorageEvacuator, talos TalosClient,
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) error {
	if ev == nil || !apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionStorageEvacuation) {
		return nil
	}

	node, err := resolveNodeName(ctx, c, talos, m)
	if err != nil {
		return err
	}

	return endMachineEvacuation(ctx, c, ev, cluster, m, node)
}

// setEvacuationCondition writes conditionStorageEvacuation and persists m's status.
func setEvacuationCondition(
	ctx context.Context, c client.Client, m *ichorv1alpha1.TalosMachine, status metav1.ConditionStatus, reason, message string,
) error {
	return setMachineCondition(ctx, c, c, m, metav1.Condition{
		Type: conditionStorageEvacuation, Status: status, Reason: reason, Message: message,
	})
}
