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
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
}

var (
	longhornNodeGVK        = schema.GroupVersionKind{Group: "longhorn.io", Version: "v1beta2", Kind: "Node"}
	longhornReplicaListGVK = schema.GroupVersionKind{Group: "longhorn.io", Version: "v1beta2", Kind: "ReplicaList"}
)

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

	var matches []*ichorv1alpha1.TalosStorage

	for i := range storages.Items {
		s := &storages.Items[i]
		if s.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		sel, err := metav1.LabelSelectorAsSelector(&s.Spec.Nodes)
		if err != nil {
			return nil, fmt.Errorf("parsing TalosStorage %q's node selector: %w", s.Name, err)
		}

		if sel.Matches(labels.Set(n.Labels)) {
			matches = append(matches, s)
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	// More than one TalosStorage selecting the same node is a
	// misconfiguration this does not reject, just resolves the same way on
	// every reconcile: sorted by name, first wins.
	slices.SortFunc(matches, func(a, b *ichorv1alpha1.TalosStorage) int { return strings.Compare(a.Name, b.Name) })
	chosen := matches[0]

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

// Begin implements StorageEvacuator.
func (e *LonghornEvacuator) Begin(ctx context.Context, target *evacuationTarget, node string) error {
	allowScheduling, evictionRequested := target.hook.BeginEvacuation()

	return e.writeNodeEvacuation(ctx, target, node, allowScheduling, evictionRequested)
}

// End implements StorageEvacuator.
func (e *LonghornEvacuator) End(ctx context.Context, target *evacuationTarget, node string) error {
	allowScheduling, evictionRequested := target.hook.EndEvacuation()

	return e.writeNodeEvacuation(ctx, target, node, allowScheduling, evictionRequested)
}

// writeNodeEvacuation writes allowScheduling then evictionRequested to
// node's nodes.longhorn.io object, as two separate updates in that order --
// Longhorn's own admission webhook rejects a write that leaves
// evictionRequested true while allowScheduling is also true (ADR 0025,
// verified against longhorn-manager v1.9.1's webhook/resources/node/validator.go).
func (e *LonghornEvacuator) writeNodeEvacuation(
	ctx context.Context, target *evacuationTarget, node string, allowScheduling, evictionRequested bool,
) error {
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(longhornNodeGVK)

	if err := e.Get(ctx, types.NamespacedName{Namespace: target.namespace, Name: node}, &obj); err != nil {
		return fmt.Errorf("reading Longhorn node %q: %w", node, err)
	}

	if err := unstructured.SetNestedField(obj.Object, allowScheduling, "spec", "allowScheduling"); err != nil {
		return err
	}

	if err := e.Update(ctx, &obj); err != nil {
		return fmt.Errorf("writing Longhorn node %q's allowScheduling: %w", node, err)
	}

	if err := unstructured.SetNestedField(obj.Object, evictionRequested, "spec", "evictionRequested"); err != nil {
		return err
	}

	if err := e.Update(ctx, &obj); err != nil {
		return fmt.Errorf("writing Longhorn node %q's evictionRequested: %w", node, err)
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
