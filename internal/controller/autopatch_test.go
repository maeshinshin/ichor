//nolint:goconst // the fixtures repeat the same version strings on purpose
package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

const autoPatchNS = "auto-patch-test-ns"

var _ = Describe("AutoPatcher", Ordered, func() {
	var (
		rec     *record.FakeRecorder
		cluster *ichorv1alpha1.TalosCluster
		clock   time.Time
		enabled bool
		window  string
	)

	BeforeAll(func() {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metaOf(autoPatchNS)})).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))
	})

	patcher := func() *AutoPatcher {
		return &AutoPatcher{
			Client: k8sClient, Actor: k8sClient, Reader: k8sClient, Recorder: rec, Now: func() time.Time { return clock },
			Settings: &settings.Reader{Client: k8sClient, Fallback: settings.Fallback{AutoPatch: settings.AutoPatch{Enabled: enabled, Window: window}}},
		}
	}

	pass := func() { ExpectWithOffset(1, patcher().Pass(ctx)).To(Succeed()) }

	inst := func() *ichorv1alpha1.IchorInstallation {
		var i ichorv1alpha1.IchorInstallation
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &i)).To(Succeed())

		return &i
	}

	operations := func() []ichorv1alpha1.TalosOperation {
		var l ichorv1alpha1.TalosOperationList
		ExpectWithOffset(1, k8sClient.List(ctx, &l, client.InNamespace(autoPatchNS))).To(Succeed())

		return l.Items
	}

	setPhase := func(op *ichorv1alpha1.TalosOperation, phase, failure string) {
		ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(op), op)).To(Succeed())
		op.Status.Phase, op.Status.FailureReason = phase, failure
		ExpectWithOffset(1, k8sClient.Status().Update(ctx, op)).To(Succeed())
	}

	talosVersion := func() string {
		var c ichorv1alpha1.TalosCluster
		ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &c)).To(Succeed())

		return c.Spec.TalosVersion
	}

	BeforeEach(func() {
		rec = record.NewFakeRecorder(50)
		clock, enabled, window = time.Now().UTC(), true, ""

		i := createInstallation("v0.1.0")
		at := metav1.NewTime(clock.Add(-time.Hour))
		i.Status.UpgradeTargets = &ichorv1alpha1.IchorUpgradeTargets{CheckedAt: &at, Talos: []string{"v1.14.5", "v1.14.4", "v1.14.3"}}
		Expect(k8sClient.Status().Update(ctx, i)).To(Succeed())

		spec := newClusterSpec("auto-patch-secret", "v1.14")
		spec.KubernetesVersion = "v1.36.1"
		cluster = &ichorv1alpha1.TalosCluster{GenerateName: "auto-", Namespace: autoPatchNS, Spec: spec}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{Detected: ciliumName, DetectedVersion: "1.20.4"}
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		m := &ichorv1alpha1.TalosMachine{
			GenerateName: "auto-m-", Namespace: autoPatchNS,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Identity: ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + cluster.Name},
				Endpoint: storageTestEndpoint, Role: roleWorker, ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Converged"})
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		DeferCleanup(func() {
			for _, op := range operations() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &op))).To(Succeed())
			}

			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, m))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed())
		})
	})

	It("starts one Talos patch as an operation marked automatic and never a second while it runs", func() {
		pass()

		ops := operations()
		Expect(ops).To(HaveLen(1))

		op := ops[0]
		Expect(op.Spec.Kind).To(Equal(ichorv1alpha1.TalosOperationKindTalos))
		Expect(op.Spec.ClusterRef.Name).To(Equal(cluster.Name))
		Expect(op.Annotations).To(HaveKeyWithValue(ichorv1alpha1.OperationAutoPatchAnnotation, "true"))
		Expect(op.Annotations).To(HaveKeyWithValue(ichorv1alpha1.OperationStartedByAnnotation, ichorv1alpha1.AutoPatchStarter))
		Expect(op.Annotations).To(HaveKeyWithValue(ichorv1alpha1.OperationFromVersionAnnotation, "v1.14.3"))
		Expect(talosVersion()).To(Equal("v1.14.5"), "the newest patch of the running minor is written for the executor")

		Expect(inst().Status.AutoPatch).NotTo(BeNil())
		Expect(inst().Status.AutoPatch.LastOperation).To(Equal(autoPatchNS + "/" + op.Name))
		Expect(rec.Events).To(Receive(ContainSubstring("AutoPatchStarted")))

		clock = clock.Add(10 * time.Minute)
		pass()
		Expect(operations()).To(HaveLen(1), "an unfinished operation holds the cluster")
	})

	It("starts nothing while the setting is off, outside the window, or with a machine not Ready", func() {
		enabled = false
		pass()
		Expect(operations()).To(BeEmpty())

		enabled, window = true, clock.Add(2*time.Hour).Format("15:04")+"-"+clock.Add(3*time.Hour).Format("15:04")
		pass()
		Expect(operations()).To(BeEmpty())

		window = ""

		var ms ichorv1alpha1.TalosMachineList
		Expect(k8sClient.List(ctx, &ms, client.InNamespace(autoPatchNS))).To(Succeed())

		for i := range ms.Items {
			apimeta.SetStatusCondition(&ms.Items[i].Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionFalse, Reason: "RebootRequired"})
			Expect(k8sClient.Status().Update(ctx, &ms.Items[i])).To(Succeed())
		}

		pass()
		Expect(operations()).To(BeEmpty())
	})

	It("halts when an automatic operation pauses, never retries, and resumes on an acknowledgement", func() {
		pass()
		Expect(operations()).To(HaveLen(1))

		op := operations()[0]
		ref := autoPatchNS + "/" + op.Name
		setPhase(&op, ichorv1alpha1.TalosOperationPhasePaused, "machine m1: drain timed out")

		pass()

		i := inst()
		Expect(i.Status.AutoPatch.HaltedOperation).To(Equal(ref))
		Expect(apimeta.IsStatusConditionTrue(i.Status.Conditions, ichorv1alpha1.ConditionAutoPatchHalted)).To(BeTrue())
		Expect(rec.Events).To(Receive(ContainSubstring("AutoPatchStarted")))
		Expect(rec.Events).To(Receive(And(ContainSubstring("AutoPatchHalted"), ContainSubstring("drain timed out"))))

		// Fixing nothing: a later pass neither retries nor starts the next component.
		clock = clock.Add(time.Hour)
		pass()
		Expect(operations()).To(HaveLen(1))
		Expect(inst().Status.AutoPatch.HaltedOperation).To(Equal(ref))

		// A person acknowledges it; the controller never edits spec, so this is the person's write.
		i = inst()
		i.Spec.AutoPatch = &ichorv1alpha1.IchorAutoPatchSettings{AcknowledgedHalt: ref}
		Expect(k8sClient.Update(ctx, i)).To(Succeed())

		pass()

		i = inst()
		Expect(i.Status.AutoPatch.HaltedOperation).To(BeEmpty())
		Expect(apimeta.FindStatusCondition(i.Status.Conditions, ichorv1alpha1.ConditionAutoPatchHalted)).To(BeNil())
		Expect(operations()).To(HaveLen(1), "the paused operation still holds the cluster, so nothing new starts")
		Expect(rec.Events).To(Receive(ContainSubstring("AutoPatchResumed")))
	})

	It("resumes when a later manual upgrade completes, and when the halting operation is deleted", func() {
		pass()

		op := operations()[0]
		ref := autoPatchNS + "/" + op.Name
		setPhase(&op, ichorv1alpha1.TalosOperationPhasePaused, "failed")
		pass()
		Expect(inst().Status.AutoPatch.HaltedOperation).To(Equal(ref))

		manual := &ichorv1alpha1.TalosOperation{
			GenerateName: "manual-", Namespace: autoPatchNS,
			Spec: ichorv1alpha1.TalosOperationSpec{ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindTalos},
		}
		Expect(k8sClient.Create(ctx, manual)).To(Succeed())

		pass()
		Expect(inst().Status.AutoPatch.HaltedOperation).To(Equal(ref), "a manual operation that has not completed does not end the halt")

		setPhase(manual, ichorv1alpha1.TalosOperationPhaseCompleted, "")
		pass()
		Expect(inst().Status.AutoPatch.HaltedOperation).To(BeEmpty())
	})
})
