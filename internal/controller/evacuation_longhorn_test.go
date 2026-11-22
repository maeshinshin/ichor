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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

const (
	longhornTestNode = "lh-node"
	longhornDenied   = "need to disable scheduling on node for node eviction, or cancel eviction to enable scheduling on this node"
)

// longhornNodeFake is a client holding one Longhorn Node whose Update runs Longhorn's webhook rule:
// allowScheduling and evictionRequested may not both be true. updates records every accepted write.
func longhornNodeFake(allowScheduling, evictionRequested bool, updates *[]map[string]any) client.Client {
	obj := &unstructured.Unstructured{Object: map[string]any{
		keySpec: map[string]any{fieldAllowScheduling: allowScheduling, fieldEvictionRequested: evictionRequested},
	}}
	obj.SetGroupVersionKind(longhornNodeGVK)
	obj.SetNamespace(defaultLonghornNamespace)
	obj.SetName(longhornTestNode)

	return interceptor.NewClient(
		fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(obj).Build(),
		interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
			u := o.(*unstructured.Unstructured)
			allow, _, _ := unstructured.NestedBool(u.Object, "spec", fieldAllowScheduling)
			evict, _, _ := unstructured.NestedBool(u.Object, "spec", fieldEvictionRequested)

			if allow && evict {
				return apierrors.NewBadRequest("admission webhook \"validator.longhorn.io\" denied the request: " + longhornDenied)
			}

			*updates = append(*updates, map[string]any{fieldAllowScheduling: allow, fieldEvictionRequested: evict})

			return c.Update(ctx, o, opts...)
		}},
	)
}

func longhornTestTarget() *evacuationTarget {
	hook, err := storage.NewEvacuationHook(storage.LonghornV1)
	Expect(err).NotTo(HaveOccurred())

	return &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace}
}

var _ = Describe("LonghornEvacuator Begin and End", func() {
	It("begins by disabling scheduling, then requesting eviction", func() {
		var updates []map[string]any

		e := &LonghornEvacuator{Client: longhornNodeFake(true, false, &updates)}
		Expect(e.Begin(ctx, longhornTestTarget(), longhornTestNode)).To(Succeed())
		Expect(updates).To(Equal([]map[string]any{
			{fieldAllowScheduling: false, fieldEvictionRequested: false},
			{fieldAllowScheduling: false, fieldEvictionRequested: true},
		}))
	})

	It("ends by cancelling the eviction first, then enabling scheduling -- the webhook refuses the reverse", func() {
		var updates []map[string]any

		e := &LonghornEvacuator{Client: longhornNodeFake(false, true, &updates)}
		Expect(e.End(ctx, longhornTestTarget(), longhornTestNode)).To(Succeed())
		Expect(updates).To(Equal([]map[string]any{
			{fieldAllowScheduling: false, fieldEvictionRequested: false},
			{fieldAllowScheduling: true, fieldEvictionRequested: false},
		}))
	})

	It("treats a missing Longhorn Node object as nothing to evacuate", func() {
		var updates []map[string]any

		e := &LonghornEvacuator{Client: longhornNodeFake(true, false, &updates)}
		Expect(e.Begin(ctx, longhornTestTarget(), "no-such-node")).To(Succeed())
		Expect(e.End(ctx, longhornTestTarget(), "no-such-node")).To(Succeed())
		Expect(updates).To(BeEmpty())
	})

	It("treats an uninstalled Longhorn CRD as nothing to evacuate, and no replicas remaining", func() {
		noMatch := interceptor.NewClient(fake.NewClientBuilder().Build(), interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Group: longhornGroup, Kind: "Node"}}
			},
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Group: longhornGroup, Kind: "Replica"}}
			},
		})

		e := &LonghornEvacuator{Client: noMatch}
		Expect(e.Begin(ctx, longhornTestTarget(), longhornTestNode)).To(Succeed())
		Expect(e.End(ctx, longhornTestTarget(), longhornTestNode)).To(Succeed())

		n, err := e.ReplicasRemaining(ctx, longhornTestTarget(), longhornTestNode)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero())
	})

	It("still fails on any other read error", func() {
		boom := errors.New("boom")
		failing := interceptor.NewClient(fake.NewClientBuilder().Build(), interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return boom
			},
		})

		e := &LonghornEvacuator{Client: failing}
		Expect(e.Begin(ctx, longhornTestTarget(), longhornTestNode)).To(MatchError(boom))
	})
})

// longhornElsewhere is the API as seen when Longhorn is installed outside longhorn-system: no Longhorn Node
// object is found for node (the one in longhorn-system is NotFound), and a replica in another namespace still
// names node. Every other object is the real envtest API, so TalosStorage and Node resolve as they do in production.
type longhornElsewhere struct {
	client.Client

	replica unstructured.Unstructured
}

func newLonghornElsewhere(node string) longhornElsewhere {
	replica := unstructured.Unstructured{Object: map[string]any{keySpec: map[string]any{"nodeID": node}}}
	replica.SetNamespace("longhorn-elsewhere")
	replica.SetName("replica-elsewhere")

	return longhornElsewhere{Client: k8sClient, replica: replica}
}

func (l longhornElsewhere) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if obj.GetObjectKind().GroupVersionKind().Group == longhornGroup {
		return apierrors.NewNotFound(schema.GroupResource{Group: longhornGroup, Resource: "nodes"}, key.Name)
	}

	return l.Client.Get(ctx, key, obj, opts...)
}

func (l longhornElsewhere) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if ul, ok := list.(*unstructured.UnstructuredList); ok && ul.GroupVersionKind().Group == longhornGroup {
		ul.Items = []unstructured.Unstructured{l.replica}

		return nil
	}

	return l.Client.List(ctx, list, opts...)
}

var _ = Describe("storage evacuation refuses a drain it cannot evacuate", func() {
	It("refuses the drain when a TalosStorage governs the node, Longhorn's Node is not in longhorn-system, and a replica still names it", func() {
		cluster := newOpTestCluster()
		newTestStorageNode("node-refuse1", map[string]string{testStorageLabelKey: testStorageLabelValue})
		newTestStorage(cluster.Name, metav1.LabelSelector{MatchLabels: map[string]string{testStorageLabelKey: testStorageLabelValue}}, nil)
		m := newOpTestMachine(cluster.Name, roleControlPlane, "refuse1")

		_, err := evacuateMachine(ctx, k8sClient, &LonghornEvacuator{Client: newLonghornElsewhere("node-refuse1")},
			cluster, m, "node-refuse1", "removal")
		Expect(err).To(MatchError(ContainSubstring("refusing to evacuate node \"node-refuse1\"")))
	})
})

// routedClient sends every Longhorn call to lh and everything else to the envtest API, so TalosStorage and the
// Kubernetes Node resolve as in production while Longhorn's objects are whatever the test puts in lh.
type routedClient struct {
	client.Client

	lh client.Client
}

func longhornRouted(lh client.Client) client.Client { return routedClient{Client: k8sClient, lh: lh} }

func isLonghorn(obj runtime.Object) bool {
	return obj.GetObjectKind().GroupVersionKind().Group == longhornGroup
}

func (r routedClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if isLonghorn(obj) {
		return r.lh.Get(ctx, key, obj, opts...)
	}

	return r.Client.Get(ctx, key, obj, opts...)
}

func (r routedClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if isLonghorn(list) {
		return r.lh.List(ctx, list, opts...)
	}

	return r.Client.List(ctx, list, opts...)
}

func (r routedClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if isLonghorn(obj) {
		return r.lh.Update(ctx, obj, opts...)
	}

	return r.Client.Update(ctx, obj, opts...)
}

// longhornFakeIn is a Longhorn install in ns: a Node object for node and one replica still naming it.
func longhornFakeIn(ns, node string, more ...string) client.WithWatch {
	b := fake.NewClientBuilder().WithScheme(runtime.NewScheme())

	for _, n := range append([]string{ns}, more...) {
		nodeObj := &unstructured.Unstructured{Object: map[string]any{
			keySpec: map[string]any{fieldAllowScheduling: true, fieldEvictionRequested: false},
		}}
		nodeObj.SetGroupVersionKind(longhornNodeGVK)
		nodeObj.SetNamespace(n)
		nodeObj.SetName(node)

		replica := &unstructured.Unstructured{Object: map[string]any{keySpec: map[string]any{"nodeID": node}}}
		replica.SetGroupVersionKind(longhornReplicaListGVK.GroupVersion().WithKind("Replica"))
		replica.SetNamespace(n)
		replica.SetName("replica-" + node)

		b = b.WithObjects(nodeObj, replica)
	}

	return b.Build()
}

var _ = Describe("storage evacuation finds the namespace Longhorn runs in", func() {
	setup := func(suffix string) (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, string) {
		cluster := newOpTestCluster()
		node := "node-ns-" + suffix
		newTestStorageNode(node, map[string]string{testStorageLabelKey: testStorageLabelValue})
		newTestStorage(cluster.Name, metav1.LabelSelector{MatchLabels: map[string]string{testStorageLabelKey: testStorageLabelValue}}, nil)

		return cluster, newOpTestMachine(cluster.Name, roleControlPlane, suffix), node
	}

	It("requests the eviction in a namespace other than longhorn-system, and the drain waits for the replica", func() {
		cluster, m, node := setup("other")
		lh := longhornFakeIn("storage-ns", node)
		e := &LonghornEvacuator{Client: longhornRouted(lh)}

		progress, err := evacuateMachine(ctx, k8sClient, e, cluster, m, node, "removal")
		Expect(err).NotTo(HaveOccurred())
		Expect(progress).To(Equal(storage.EvacuationWaiting))

		var got unstructured.Unstructured
		got.SetGroupVersionKind(longhornNodeGVK)
		Expect(lh.Get(ctx, client.ObjectKey{Namespace: "storage-ns", Name: node}, &got)).To(Succeed())

		evict, _, _ := unstructured.NestedBool(got.Object, "spec", fieldEvictionRequested)
		Expect(evict).To(BeTrue())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(m), m)).To(Succeed())
		progress, err = evacuateMachine(ctx, k8sClient, e, cluster, m, node, "removal")
		Expect(err).NotTo(HaveOccurred())
		Expect(progress).To(Equal(storage.EvacuationWaiting))
	})

	It("refuses when Longhorn runs in two namespaces, and requests no eviction", func() {
		cluster, m, node := setup("two")
		lh := longhornFakeIn("storage-a", node, "storage-b")

		_, err := evacuateMachine(ctx, k8sClient, &LonghornEvacuator{Client: longhornRouted(lh)}, cluster, m, node, "removal")
		Expect(err).To(MatchError(errMultipleLonghorn))
		Expect(err).To(MatchError(ContainSubstring("storage-a, storage-b")))
	})

	It("does not drain when listing Longhorn's nodes fails", func() {
		cluster, m, node := setup("fail")
		boom := errors.New("boom")
		failing := interceptor.NewClient(longhornFakeIn("storage-ns", node), interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, o ...client.ListOption) error {
				if l.GetObjectKind().GroupVersionKind() == longhornNodeListGVK {
					return boom
				}

				return c.List(ctx, l, o...)
			},
		})

		progress, err := evacuateMachine(ctx, k8sClient, &LonghornEvacuator{Client: longhornRouted(failing)}, cluster, m, node, "removal")
		Expect(err).To(MatchError(boom))
		Expect(progress).NotTo(Equal(storage.EvacuationDone))
	})
})
