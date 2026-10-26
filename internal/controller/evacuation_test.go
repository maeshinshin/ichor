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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// testStorageLabelKey and testStorageLabelValue are the Node label every
// test below selects (or doesn't) a TalosStorage's spec.nodes on (goconst).
const (
	testStorageLabelKey   = "storage"
	testStorageLabelValue = "yes"
)

// newTestStorageNode creates a Node carrying labels, for TalosStorage's
// own node selector to match against.
func newTestStorageNode(name string, labels map[string]string) {
	n := &corev1.Node{}
	n.Name = name
	n.Labels = labels
	Expect(k8sClient.Create(ctx, n)).To(Succeed())
}

// newTestStorage creates a TalosStorage for clusterName, selecting nodes by selector.
func newTestStorage(clusterName string, selector metav1.LabelSelector, timeout *metav1.Duration) {
	s := &ichorv1alpha1.TalosStorage{
		GenerateName: "storage-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosStorageSpec{
			ClusterRef:        corev1.LocalObjectReference{Name: clusterName},
			Nodes:             selector,
			DiskPreset:        storage.LonghornV1,
			DiskSelector:      "disk.transport == 'nvme'",
			EvacuationTimeout: timeout,
		},
	}
	Expect(k8sClient.Create(ctx, s)).To(Succeed())
}

var _ = Describe("LonghornEvacuator.Resolve", func() {
	It("finds the TalosStorage whose node selector matches the given node, with the hook's own default timeout", func() {
		cluster := newOpTestCluster()
		newTestStorageNode("node-resolve1", map[string]string{testStorageLabelKey: testStorageLabelValue})
		newTestStorage(cluster.Name, metav1.LabelSelector{MatchLabels: map[string]string{testStorageLabelKey: testStorageLabelValue}}, nil)

		e := &LonghornEvacuator{Client: k8sClient}
		target, err := e.Resolve(ctx, cluster, "node-resolve1")
		Expect(err).NotTo(HaveOccurred())
		Expect(target).NotTo(BeNil())
		Expect(target.timeout).To(Equal(defaultEvacuationTimeout))
		Expect(target.namespace).To(Equal(defaultLonghornNamespace))
	})

	It("uses the TalosStorage's own evacuationTimeout when set", func() {
		cluster := newOpTestCluster()
		newTestStorageNode("node-resolve2", map[string]string{testStorageLabelKey: testStorageLabelValue})
		newTestStorage(cluster.Name, metav1.LabelSelector{MatchLabels: map[string]string{testStorageLabelKey: testStorageLabelValue}},
			&metav1.Duration{Duration: 3 * time.Minute})

		e := &LonghornEvacuator{Client: k8sClient}
		target, err := e.Resolve(ctx, cluster, "node-resolve2")
		Expect(err).NotTo(HaveOccurred())
		Expect(target.timeout).To(Equal(3 * time.Minute))
	})

	It("returns nil, nil when no TalosStorage selects the node -- nothing to evacuate", func() {
		cluster := newOpTestCluster()
		newTestStorageNode("node-resolve3", map[string]string{testStorageLabelKey: "no"})
		newTestStorage(cluster.Name, metav1.LabelSelector{MatchLabels: map[string]string{testStorageLabelKey: testStorageLabelValue}}, nil)

		e := &LonghornEvacuator{Client: k8sClient}
		target, err := e.Resolve(ctx, cluster, "node-resolve3")
		Expect(err).NotTo(HaveOccurred())
		Expect(target).To(BeNil())
	})

	It("returns nil, nil when no TalosStorage exists at all for the cluster -- the common, no-storage-system case", func() {
		cluster := newOpTestCluster()
		newTestStorageNode("node-resolve4", nil)

		e := &LonghornEvacuator{Client: k8sClient}
		target, err := e.Resolve(ctx, cluster, "node-resolve4")
		Expect(err).NotTo(HaveOccurred())
		Expect(target).To(BeNil())
	})
})
