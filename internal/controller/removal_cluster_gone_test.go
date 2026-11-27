package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// An uninstall deletes the TalosCluster first and then its machines: the nodes keep running, so
// no machine's removal may drain it, reset it or take it out of etcd.
var _ = Describe("a removal whose TalosCluster is deleting or gone", func() {
	release := func(deleteCluster func(*ichorv1alpha1.TalosCluster)) {
		cps := newThreeControlPlanes()
		setHostname(cps[0], "node-uninstall")

		fake := &fakeTalosClient{nodeName: "node-uninstall"}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: record.NewFakeRecorder(10),
		}
		reconcileEach(r, cps)

		var cluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: cps[0].Spec.ClusterRef.Name}, &cluster)).To(Succeed())
		deleteCluster(&cluster)

		for _, cp := range cps {
			Expect(k8sClient.Delete(ctx, cp)).To(Succeed())
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cp.Namespace, Name: cp.Name}, &ichorv1alpha1.TalosMachine{})
			Expect(err).To(MatchError(apierrors.IsNotFound, "the machine should be gone"))
		}

		Expect(fake.resetCalls).To(BeZero())
		Expect(fake.etcdLeaveCalls).To(BeZero())
		Expect(fake.etcdRemoveIDs).To(BeEmpty())
	}

	It("loses its finalizer without a reset or an etcd step when the cluster is gone", func() {
		release(func(c *ichorv1alpha1.TalosCluster) { Expect(k8sClient.Delete(ctx, c)).To(Succeed()) })
	})

	It("loses its finalizer without a reset or an etcd step when the cluster is deleting", func() {
		release(func(c *ichorv1alpha1.TalosCluster) {
			controllerutil.AddFinalizer(c, "test.ichor.maesh.dev/hold")
			Expect(k8sClient.Update(ctx, c)).To(Succeed())
			DeferCleanup(func() {
				controllerutil.RemoveFinalizer(c, "test.ichor.maesh.dev/hold")
				_ = k8sClient.Update(ctx, c)
			})
			Expect(k8sClient.Delete(ctx, c)).To(Succeed())
		})
	})
})
