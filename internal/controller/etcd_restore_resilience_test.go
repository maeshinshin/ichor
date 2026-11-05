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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func watchClient() client.WithWatch {
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())

	return c
}

// workerNode is the Kubernetes node name of the worker that is not wiped.
const workerNode = "wk-1"

var _ = Describe("TalosOperation etcd-restore after a restart of the controller", func() {
	BeforeEach(func() {
		restoreTailInterval = time.Millisecond
		resumeBackoffStart, resumeBackoffMax = time.Millisecond, 5*time.Millisecond
	})

	It("keeps retrying the recreation while the override webhook, served by this process, refuses connections", func() {
		f := newRestoreFixture("")
		f.wipeHooks()
		op := f.newOp(nil)
		f.reconcile(op)

		f.runOK(f.lastJob(), 5)
		dropOperation(op)

		var creates atomic.Int32

		restarted := f.reconciler("")
		restarted.Client = interceptor.NewClient(watchClient(), interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*ichorv1alpha1.TalosOperation); ok && creates.Add(1) <= 3 {
					return errors.New(`Internal error occurred: failed calling webhook "voverrideop-v1alpha1.kb.io": dial tcp 10.0.0.1:443: connect: connection refused`)
				}

				return c.Create(ctx, obj, opts...)
			},
		})

		Expect(restarted.ResumeEtcdRestores(ctx)).NotTo(Succeed(), "one pass reports what it could not do")

		Expect(restarted.ResumeEtcdRestoresUntilDone(ctx)).To(Succeed())
		Expect(creates.Load()).To(BeNumerically(">", 3))
		Expect(fetchOperation(op).Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))
	})

	It("stops retrying when the context ends, and when there is no marker it returns at once", func() {
		f := newRestoreFixture("")
		bounded, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		Expect(f.reconciler("").ResumeEtcdRestoresUntilDone(bounded)).To(Succeed())

		op := f.newOp(nil)
		f.reconcile(op)
		dropOperation(op)

		r := f.reconciler("")
		r.Client = interceptor.NewClient(watchClient(), interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				return errors.New("connection refused")
			},
		})

		cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		Expect(r.ResumeEtcdRestoresUntilDone(cctx)).To(Succeed())
		Expect(f.marker()).NotTo(BeNil(), "the marker stays for the next start")
	})

	// restoreWithWorker runs the restore up to the point where the API is back and the control planes
	// are members again, with one worker that was not wiped.
	restoreWithWorker := func(kc *kubefake.Clientset) (f *restoreFixture, op *ichorv1alpha1.TalosOperation, r *TalosOperationReconciler, worker *fakeTalosClient) {
		f = newRestoreFixture("")
		f.wipeHooks()

		w := newOpTestMachine(f.backup.cluster.Name, roleWorker, "rs-w1")
		setHostname(w, "wk-1")

		worker = &fakeTalosClient{}
		f.fakes[w.Name] = worker

		op = f.newOp(nil)
		f.reconcile(op)
		_, err := f.run(f.lastJob(), 5)
		Expect(err).NotTo(HaveOccurred())
		dropOperation(op)

		r = f.reconciler("")
		r.KubeClient = kc
		Expect(r.ResumeEtcdRestores(ctx)).To(Succeed())
		f.members(f.hosts()...)

		return f, op, r, worker
	}

	kubeProxyCluster := func() *kubefake.Clientset {
		kp := map[string]string{labelK8sApp: kubeProxyName}
		pod := func(ns, name, node string, labels map[string]string) *corev1.Pod {
			return &corev1.Pod{Namespace: ns, Name: name, Labels: labels, Spec: corev1.PodSpec{NodeName: node}}
		}

		// The node reports Ready with a heartbeat after the kubelet restart.
		node := &corev1.Node{Name: "wk-1", Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(time.Now().Add(time.Hour)),
		}}}}

		return kubefake.NewClientset(
			node,
			&appsv1.DaemonSet{
				Namespace: kubeSystemNamespace, Name: kubeProxyName,
				Spec: appsv1.DaemonSetSpec{Selector: &metav1.LabelSelector{MatchLabels: kp}},
			},
			pod("kube-system", "kube-proxy-w", "wk-1", kp),
			pod("kube-system", "kube-proxy-a", restoreHostA, kp),
			pod("other", "kube-proxy-other", "wk-1", kp),
			pod("kube-system", "coredns-w", "wk-1", map[string]string{"k8s-app": "kube-dns"}),
		)
	}

	podNames := func(kc *kubefake.Clientset, ns string) []string {
		l, err := kc.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		names := make([]string, 0, len(l.Items))
		for _, p := range l.Items {
			names = append(names, p.Name)
		}

		return names
	}

	It("restarts the kubelet of a node that was not wiped, then its kube-proxy pod, and moves on only when the new pod is Ready", func() {
		kc := kubeProxyCluster()
		_, op, r, worker := restoreWithWorker(kc)

		got := reconcileFetch(r, op)
		Expect(worker.serviceRestarts).To(Equal([]string{compKubelet}), "the kubelet is restarted through Talos first")
		Expect(podNames(kc, "kube-system")).To(ContainElement("kube-proxy-w"), "the pod is not deleted before the kubelet is back")
		Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))

		got = reconcileFetch(r, op)
		Expect(worker.serviceRestarts).To(HaveLen(1), "the kubelet is restarted once")
		Expect(podNames(kc, "kube-system")).To(ConsistOf("kube-proxy-a", "coredns-w"), "only the worker's kube-proxy pod went")
		Expect(podNames(kc, "other")).To(ConsistOf("kube-proxy-other"))
		Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining), "no replacement yet: the step is not done")

		_, err := kc.CoreV1().Pods("kube-system").Create(ctx, &corev1.Pod{
			Namespace: "kube-system", Name: "kube-proxy-w2", Labels: map[string]string{labelK8sApp: kubeProxyName},
			CreationTimestamp: metav1.NewTime(time.Now().Add(time.Hour)),
			Spec:              corev1.PodSpec{NodeName: "wk-1"},
			Status:            corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		got = reconcileFetch(r, op)
		Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))
	})

	It("also restarts the CNI's pod on a node that was not wiped, and waits for its replacement", func() {
		kc := kubeProxyCluster()
		cl := map[string]string{labelK8sApp: ciliumName}

		_, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Create(ctx, &appsv1.DaemonSet{
			Namespace: kubeSystemNamespace, Name: ciliumName,
			Spec: appsv1.DaemonSetSpec{Selector: &metav1.LabelSelector{MatchLabels: cl}},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, err = kc.CoreV1().Pods(kubeSystemNamespace).Create(ctx, &corev1.Pod{
			Namespace: kubeSystemNamespace, Name: "cilium-w", Labels: cl, Spec: corev1.PodSpec{NodeName: workerNode},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		_, op, r, _ := restoreWithWorker(kc)

		reconcileFetch(r, op)
		reconcileFetch(r, op)
		Expect(podNames(kc, "kube-system")).NotTo(ContainElement("cilium-w"), "the CNI pod of the worker went with kube-proxy's")
		Expect(podNames(kc, "kube-system")).To(ContainElement("coredns-w"))
	})

	It("says so, rather than claiming success, when the node does not converge", func() {
		old := agentsBound
		agentsBound = time.Millisecond

		DeferCleanup(func() { agentsBound = old })

		kc := kubeProxyCluster()
		_, op, r, _ := restoreWithWorker(kc)

		reconcileFetch(r, op)
		time.Sleep(5 * time.Millisecond)

		got := reconcileFetch(r, op)
		Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))
		Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("wk-1"))
		Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("kube-proxy"))
	})

	It("recognises the controller's own control plane when its spec.hostname is empty, and wipes it last", func() {
		f := newRestoreFixture(restoreHostA)
		own := f.cps[restoreHostA]
		setHostname(own, "")
		f.fake(restoreHostA).nodeName = restoreHostA

		op := f.newOp(nil)
		got := f.reconcile(op)

		order := make([]string, 0, 2)
		for _, n := range f.lastJob().order {
			order = append(order, n.name)
		}

		Expect(order).NotTo(ContainElement(own.Name), "the controller's node is not wiped in the first pass")
		Expect(order).To(HaveLen(2))
		Expect(f.marker().Own).To(Equal(own.Name))
		Expect(f.marker().Nodes).To(HaveKeyWithValue(own.Name, restoreHostA))
		Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("where this controller runs"))
		Expect(got.Status.EtcdRestore.Message).To(ContainSubstring(restoreHostA))
	})
})

var _ = Describe("TalosOperation etcd-restore failures", func() {
	BeforeEach(func() { restoreTailInterval = time.Millisecond })

	It("records a panic in the job as the marker's failure instead of dying silently", func() {
		f := newRestoreFixture("")
		op := f.newOp(nil)
		f.reconcile(op)

		job := f.lastJob()
		job.order[0].dial = func(context.Context) (TalosClient, error) { panic("boom") }

		job.run(ctx)

		Expect(f.marker().Failure).To(ContainSubstring("boom"))
	})

	It("keeps the job's last note in the marker, so a process that is killed leaves a trace", func() {
		f := newRestoreFixture("")
		f.wipeHooks()
		op := f.newOp(nil)
		f.reconcile(op)

		f.targetFake().recoverErr = errors.Join(ErrRecoveryNotReady, errors.New("etcd service is not ready"))

		job := f.lastJob()
		f.runOK(job, 4)
		job.recordNote(ctx)

		Expect(f.marker().LastNote).To(ContainSubstring("not ready for recovery"))
		Expect(f.marker().Failure).To(BeEmpty())

		got := f.reconcile(op)
		Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("not ready for recovery"))
	})

	It("tells the operator about ichor restore --recover-cluster when the restore fails", func() {
		f := newRestoreFixture("")
		f.wipeHooks()
		op := f.newOp(nil)
		f.reconcile(op)

		job := f.lastJob()
		job.fail(ctx, errors.New("bootstrapping etcd from the snapshot on x: refused"))
		Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepWiping))

		got := f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("refused"))
		Expect(got.Status.FailureReason).To(ContainSubstring("ichor restore --recover-cluster"))
	})
})
