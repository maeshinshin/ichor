package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("CAClientsNotRestarted re-check", func() {
	// reconcileWith raises the condition naming cilium-operator, whose Deployment has availableReplicas, and reconciles.
	reconcileWith := func(availableReplicas int32) *metav1.Condition {
		one := int32(1)
		kc := fake.NewClientset(&appsv1.Deployment{
			Namespace: kubeSystemNamespace, Name: ciliumOperatorName, Generation: 1,
			Spec:   appsv1.DeploymentSpec{Replicas: &one},
			Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: availableReplicas},
		})

		cluster := newTestCluster("caclients-secret", "v1.14")
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionCAClientsNotRestarted, Status: metav1.ConditionTrue, Reason: bundleStaleReason,
			Message: "the CAs are replaced; not back after the restart: Deployment kube-system/cilium-operator",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		_, err := reconcileCluster(&TalosClusterReconciler{Client: k8sClient, KubeClient: kc}, cluster)
		Expect(err).NotTo(HaveOccurred())

		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &got)).To(Succeed())

		return apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionCAClientsNotRestarted)
	}

	It("clears once the named workloads are back", func() {
		cond := reconcileWith(1)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonCAClientsBack))
		Expect(cond.Message).To(ContainSubstring("Deployment kube-system/cilium-operator"))
	})

	It("stays True while a named workload is still unavailable", func() {
		Expect(reconcileWith(0).Status).To(Equal(metav1.ConditionTrue))
	})
})
