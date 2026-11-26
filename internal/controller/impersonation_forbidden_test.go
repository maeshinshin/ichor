package controller

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// forbiddenActor is an impersonated client whose identity does not exist (webhook.enabled=false): every write is Forbidden.
type forbiddenActor struct{ client.Client }

var errNoIdentity = apierrors.NewForbidden(schema.GroupResource{Resource: "talosclusters"}, "x", errors.New("cannot impersonate"))

func (forbiddenActor) Create(context.Context, client.Object, ...client.CreateOption) error {
	return errNoIdentity
}

func (forbiddenActor) Update(context.Context, client.Object, ...client.UpdateOption) error {
	return errNoIdentity
}

func (forbiddenActor) Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) error {
	return errNoIdentity
}

var _ = Describe("an impersonated identity that does not exist", func() {
	It("auto-patch records AutoPatchForbidden, starts nothing and does not fail the pass", func() {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metaOf(autoPatchNS)})).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))

		rec := record.NewFakeRecorder(50)
		now := time.Now().UTC()

		i := createInstallation("v0.1.0")
		at := metav1.NewTime(now.Add(-time.Hour))
		i.Status.UpgradeTargets = &ichorv1alpha1.IchorUpgradeTargets{CheckedAt: &at, Talos: []string{patchA, patchB, testTalosVersion}}
		Expect(k8sClient.Status().Update(ctx, i)).To(Succeed())

		spec := newClusterSpec("auto-patch-secret", "v1.14")
		spec.KubernetesVersion = "v1.36.1"
		cluster := &ichorv1alpha1.TalosCluster{GenerateName: "forbidden-", Namespace: autoPatchNS, Spec: spec}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed()) })

		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{Detected: ciliumName, DetectedVersion: "1.20.4"}
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		m := &ichorv1alpha1.TalosMachine{
			GenerateName: "forbidden-m-", Namespace: autoPatchNS,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Identity: ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + cluster.Name},
				Endpoint: storageTestEndpoint, Role: roleWorker, ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Converged"})
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, m))).To(Succeed()) })

		p := &AutoPatcher{
			Client: k8sClient, Actor: forbiddenActor{k8sClient}, Reader: k8sClient, Recorder: rec, Namespace: autoPatchNS,
			Now:      func() time.Time { return now },
			Settings: &settings.Reader{Client: k8sClient, Fallback: settings.Fallback{AutoPatch: settings.AutoPatch{Enabled: true}}},
		}
		Expect(p.Pass(ctx)).To(Succeed())
		Expect(rec.Events).To(Receive(ContainSubstring("AutoPatchForbidden")))

		var ops ichorv1alpha1.TalosOperationList
		Expect(k8sClient.List(ctx, &ops, client.InNamespace(autoPatchNS))).To(Succeed())
		Expect(ops.Items).To(BeEmpty())

		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &got)).To(Succeed())
		Expect(got.Spec.TalosVersion).To(Equal(cluster.Spec.TalosVersion))
	})

	It("the consent sweep records ConsentSweepForbidden and the reconcile goes on to discovery", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.CNI.KubeProxy = modeReplaced
		cluster.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: modeReplaced}
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{testAddr}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: modeReplaced}
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		rec := record.NewFakeRecorder(10)
		dialed := 0
		r := newDiscoveryReconciler(func(context.Context, string) (MaintenanceClient, error) {
			dialed++

			return nil, errFactoryMustNotBeCalled
		})
		r.Actor, r.Discovery.Recorder = forbiddenActor{k8sClient}, rec

		_, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Events).To(Receive(ContainSubstring("ConsentSweepForbidden")))
		Expect(dialed).To(BeNumerically(">", 0), "discovery ran after the refused sweep")
	})
})
