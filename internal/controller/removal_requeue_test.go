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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

// A removal that holds its finalizer on a failure asks to be tried again: nothing else wakes the machine,
// since the status write that reports the failure is the same on every pass.
var _ = Describe("a failed step of a removal", func() {
	const requeueNode = "requeue-reset-node"

	It("requeues after EtcdMemberRemovalFailed", func() {
		cps := newThreeControlPlanes()
		setHostname(cps[0], "node-rq1")
		// No client dials: neither the machine nor a survivor can be reached.
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		reconcileEach(r, cps)

		Expect(k8sClient.Delete(ctx, cps[0])).To(Succeed())
		res, err := reconcileMachine(r, cps[0])
		Expect(err).NotTo(HaveOccurred())
		expectRemoval(fetchMachine(cps[0]), reasonEtcdMemberRemoveFail)
		Expect(res.RequeueAfter).To(Equal(nodeWaitInterval))
	})

	It("requeues after ResetFailed", func() {
		newTestNode(requeueNode, true)
		DeferCleanup(func() {
			n := &corev1.Node{}
			n.Name = requeueNode
			_ = k8sClient.Delete(ctx, n)
		})

		cps := newThreeControlPlanes()
		fake := &fakeTalosClient{nodeName: requeueNode, resetErr: fmt.Errorf("reset failed")}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: record.NewFakeRecorder(10),
		}
		reconcileEach(r, cps)

		optIntoReset(cps[0])
		res, err := reconcileMachine(r, cps[0])
		Expect(err).NotTo(HaveOccurred())
		expectRemoval(fetchMachine(cps[0]), reasonResetFailed)
		Expect(res.RequeueAfter).To(Equal(nodeWaitInterval))
	})
})
