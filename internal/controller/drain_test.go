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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func drainTestNode(name string, unschedulable bool) *corev1.Node {
	return &corev1.Node{
		Name:   name,
		Spec:   corev1.NodeSpec{Unschedulable: unschedulable},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
}

func drainTestPod(name, node, ownerKind string, mirror bool) *corev1.Pod {
	p := &corev1.Pod{
		Namespace: "default", Name: name, Annotations: map[string]string{},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	if ownerKind != "" {
		p.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "apps/v1", Kind: ownerKind, Name: "owner", UID: types.UID("uid-" + name), Controller: ptr.To(true),
		}}
	}

	if mirror {
		p.Annotations[corev1.MirrorPodAnnotationKey] = "hash"
	}

	return p
}

// drainFake is a fake clientset where evicting a pod deletes it, as a real API server does once
// the PodDisruptionBudgets allow. blocked names pods whose eviction a budget refuses (429), and
// an eviction that arrives after the reboot was requested fails the spec.
type drainFake struct {
	*kubefake.Clientset

	blocked map[string]bool
	evicted []string
}

func newDrainFake(rebootCalls func() int, objs ...runtime.Object) *drainFake {
	f := &drainFake{Clientset: kubefake.NewSimpleClientset(objs...), blocked: map[string]bool{}}
	f.Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "pods/eviction", Kind: "Eviction", Group: "policy", Version: "v1"},
	}}}

	f.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "eviction" {
			return false, nil, nil
		}

		ev, ok := a.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		if !ok {
			return false, nil, nil
		}

		if rebootCalls() != 0 {
			Fail("a pod was evicted after the reboot was requested")
		}

		if f.blocked[ev.Name] {
			return true, nil, apierrors.NewTooManyRequests("disruption budget", 1)
		}

		f.evicted = append(f.evicted, ev.Name)

		return true, nil, f.Tracker().Delete(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, ev.Namespace, ev.Name)
	})

	return f
}

func (f *drainFake) node(name string) *corev1.Node {
	n, err := f.CoreV1().Nodes().Get(context.Background(), name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	return n
}

func (f *drainFake) hasPod(name string) bool {
	_, err := f.CoreV1().Pods("default").Get(context.Background(), name, metav1.GetOptions{})

	return err == nil
}

// shortenDrain bounds one drain step by step and retries a refused eviction every 50ms, for the rest of the spec.
func shortenDrain(step time.Duration) {
	DeferCleanup(func(s, d time.Duration) { drainStepTimeout, drainEvictRetryDelay = s, d }, drainStepTimeout, drainEvictRetryDelay)

	drainStepTimeout, drainEvictRetryDelay = step, 50*time.Millisecond
}

func drainRolloutAdvancer(fake *fakeTalosClient, kube *drainFake) *configRolloutAdvancer {
	a := newRolloutAdvancer(fake)
	a.Kube = kube

	return a
}

var _ = Describe("configRolloutAdvancer drain", func() {
	owner := &appsv1.DaemonSet{Namespace: "default", Name: "owner"}

	It("cordons and drains before the reboot, skips DaemonSet and mirror pods, and uncordons once the node is back", func() {
		f := newRolloutFixture()
		m := newTestMachine(f.cluster.Name, true)
		setHostname(m, "node-dr-ok")

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		kube := newDrainFake(func() int { return fake.rebootCalls },
			drainTestNode("node-dr-ok", false), owner,
			drainTestPod("web", "node-dr-ok", "ReplicaSet", false),
			drainTestPod("agent", "node-dr-ok", "DaemonSet", false),
			drainTestPod("static", "node-dr-ok", "", true),
		)
		adv := drainRolloutAdvancer(fake, kube)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(Equal(1))
		Expect(kube.evicted).To(ConsistOf("web"), "evicted through the Eviction API, so PodDisruptionBudgets apply")
		Expect(kube.hasPod("agent")).To(BeTrue(), "DaemonSet pods are left alone")
		Expect(kube.hasPod("static")).To(BeTrue(), "mirror pods are left alone")
		Expect(kube.node("node-dr-ok").Spec.Unschedulable).To(BeTrue(), "the node stays cordoned across the reboot")

		fake.bootID, fake.current = testBootIDAfter, f.after(m)

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())

		node := kube.node("node-dr-ok")
		Expect(node.Spec.Unschedulable).To(BeFalse(), "uncordoned once the node is Ready on the new configuration")
		Expect(node.Annotations).NotTo(HaveKey(cordonedByIchorAnnotation))
		Expect(node.Annotations).NotTo(HaveKey(drainStartedAnnotation))
	})

	It("keeps draining while a PodDisruptionBudget refuses an eviction and reboots only when it lets go", func() {
		shortenDrain(2 * time.Second)

		f := newRolloutFixture()
		m := newTestMachine(f.cluster.Name, true)
		setHostname(m, "node-dr-pdb")

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		kube := newDrainFake(func() int { return fake.rebootCalls },
			drainTestNode("node-dr-pdb", false), drainTestPod("db", "node-dr-pdb", "StatefulSet", false))
		kube.blocked["db"] = true

		adv := drainRolloutAdvancer(fake, kube)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred(), "a budget holding a pod back is waiting, not a failure")
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(BeZero())
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(kube.node("node-dr-pdb").Spec.Unschedulable).To(BeTrue())

		delete(kube.blocked, "db")

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(Equal(1))
		Expect(kube.evicted).To(ConsistOf("db"))
	})

	It("pauses and uncordons without rebooting once the drain has outlasted its bound", func() {
		f := newRolloutFixture()
		m := newTestMachine(f.cluster.Name, true)
		setHostname(m, "node-dr-late")

		node := drainTestNode("node-dr-late", true)
		node.Annotations = map[string]string{
			cordonedByIchorAnnotation: "rollout",
			drainStartedAnnotation:    time.Now().Add(-2 * drainTimeout).UTC().Format(time.RFC3339),
		}

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		kube := newDrainFake(func() int { return fake.rebootCalls }, node)

		done, err := drainRolloutAdvancer(fake, kube).Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).To(MatchError(ContainSubstring("did not finish")))
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(BeZero())
		Expect(kube.node("node-dr-late").Spec.Unschedulable).To(BeFalse())
	})

	It("does not uncordon a node an administrator had cordoned", func() {
		f := newRolloutFixture()
		m := newTestMachine(f.cluster.Name, true)
		setHostname(m, "node-dr-admin")

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		kube := newDrainFake(func() int { return fake.rebootCalls }, drainTestNode("node-dr-admin", true))
		adv := drainRolloutAdvancer(fake, kube)

		_, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.rebootCalls).To(Equal(1))

		fake.bootID, fake.current = testBootIDAfter, f.after(m)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(kube.node("node-dr-admin").Spec.Unschedulable).To(BeTrue(), "the administrator's cordon is theirs to lift")
	})
})
