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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
		"spec": map[string]any{fieldAllowScheduling: allowScheduling, fieldEvictionRequested: evictionRequested},
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
