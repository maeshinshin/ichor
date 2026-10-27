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
	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

func watchClient() client.WithWatch {
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())

	return c
}

var _ = Describe("TalosOperation etcd-restore after a restart of the controller", func() {
	BeforeEach(func() {
		restoreTailInterval = time.Millisecond
		resumeBackoffStart, resumeBackoffMax = time.Millisecond, 5*time.Millisecond
	})

	It("keeps retrying the recreation while the break-glass webhook, served by this process, refuses connections", func() {
		f := newRestoreFixture("")
		f.wipeHooks()
		op := f.newOp(nil)
		f.reconcile(op)

		_, err := f.run(f.lastJob(), 5)
		Expect(err).NotTo(HaveOccurred())
		dropOperation(op)

		var creates atomic.Int32

		restarted := f.reconciler("")
		restarted.Client = interceptor.NewClient(watchClient(), interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*ichorv1alpha1.TalosOperation); ok && creates.Add(1) <= 3 {
					return errors.New(`Internal error occurred: failed calling webhook "vbreakglassop-v1alpha1.kb.io": dial tcp 10.0.0.1:443: connect: connection refused`)
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

	It("restarts kube-proxy on the nodes that were not wiped, by pod name, and nothing else", func() {
		f := newRestoreFixture("")
		f.wipeHooks()

		cp := func(name string) *corev1.Node {
			return &corev1.Node{Name: name, Labels: map[string]string{controlPlaneLabel: ""}}
		}
		pod := func(ns, name, node string, labels map[string]string) *corev1.Pod {
			return &corev1.Pod{
				Namespace: ns, Name: name, Labels: labels,
				Spec: corev1.PodSpec{NodeName: node},
			}
		}
		kp := map[string]string{labelK8sApp: kubeProxyName}
		kc := kubefake.NewClientset(
			cp(restoreHostA), cp(restoreHostB), cp(restoreHostC), &corev1.Node{Name: "wk-1"},
			&appsv1.DaemonSet{
				Namespace: kubeSystemNamespace, Name: kubeProxyName,
				Spec: appsv1.DaemonSetSpec{Selector: &metav1.LabelSelector{MatchLabels: kp}},
			},
			pod("kube-system", "kube-proxy-w", "wk-1", kp),
			pod("kube-system", "kube-proxy-a", restoreHostA, kp),
			pod("other", "kube-proxy-other", "wk-1", kp),
			pod("kube-system", "coredns-w", "wk-1", map[string]string{"k8s-app": "kube-dns"}),
		)

		op := f.newOp(nil)
		f.reconcile(op)
		_, err := f.run(f.lastJob(), 5)
		Expect(err).NotTo(HaveOccurred())
		dropOperation(op)

		restarted := f.reconciler("")
		restarted.KubeClient = kc
		Expect(restarted.ResumeEtcdRestores(ctx)).To(Succeed())
		f.members(f.hosts()...)

		got := f.reconcileWith(restarted, op)
		Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))

		left := func(ns string) []string {
			l, err := kc.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			names := make([]string, 0, len(l.Items))
			for _, p := range l.Items {
				names = append(names, p.Name)
			}

			return names
		}
		Expect(left("kube-system")).To(ConsistOf("kube-proxy-a", "coredns-w"), "only the worker's kube-proxy pod went")
		Expect(left("other")).To(ConsistOf("kube-proxy-other"))
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
		_, err := f.run(job, 4)
		Expect(err).NotTo(HaveOccurred())
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
		Expect(f.marker().Step).To(Equal(etcdbackup.MarkerWiping))

		got := f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("refused"))
		Expect(got.Status.FailureReason).To(ContainSubstring("ichor restore --recover-cluster"))
	})
})
