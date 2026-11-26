package controller

import (
	"slices"
	"testing"

	"context"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

const checkJobTestVersion = "v0.3.0"

// The installer's CiliumNetworkPolicy selects its pod by app.kubernetes.io/name (internal/installmanifest/network-policies.yaml),
// and the namespace denies everything else: a check Job under another label cannot reach the API server.
func TestInstallerCheckJobPodCarriesTheInstallerPolicyLabel(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: checkJobTestVersion, InstallerRepository: "r.example/ichor"}}
	check := NewInstallerCheckJob("ichor-system", "c", inst, "sha256:x", "", false, false)
	run := newInstallerJob("ichor-system", "r", &ichorv1alpha1.TalosOperation{}, inst, "sha256:x", "", false)

	if got, want := check.Spec.Template.Labels[nameLabel], run.Spec.Template.Labels[nameLabel]; got != want {
		t.Errorf("the check Job's pod is labelled %q, the installer's policy selects %q", got, want)
	}
}

// The check Job asks to treat a missing sign-in as satisfied only when the caller says it will turn sign-in on.
func TestInstallerCheckJobCarriesSignInPendingOnlyWhenAsked(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: checkJobTestVersion, InstallerRepository: "r.example/ichor"}}

	for pending, want := range map[bool]bool{true: true, false: false} {
		args := NewInstallerCheckJob("ichor-system", "c", inst, "sha256:x", "", false, pending).Spec.Template.Spec.Containers[0].Args

		if got := slices.Contains(args, "--sign-in-pending"); got != want {
			t.Errorf("signInPending=%v: args %v, --sign-in-pending present=%v", pending, args, got)
		}
	}
}

// TestJobBuildersTolerateControlPlane pins that every Job the controller or the CLI builds can run on a control plane
// tainted NoSchedule, as the manager does, so a cluster of only such nodes still runs them.
func TestJobBuildersTolerateControlPlane(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{}
	cluster := &ichorv1alpha1.TalosCluster{}
	jobs := map[string]*batchv1.Job{
		"run":      newInstallerJob("ichor-system", "r", &ichorv1alpha1.TalosOperation{}, inst, "sha256:x", "", false),
		"check":    NewInstallerCheckJob("ichor-system", "c", inst, "sha256:x", "", false, false),
		"cni-helm": newCNIHelmJob("ichor-system", "j", cluster, cni.KubeProxyEnabled, testCNIImage, "h", false, 0),
	}

	taint := corev1.Taint{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule}

	for name, job := range jobs {
		tolerated := false

		for i := range job.Spec.Template.Spec.Tolerations {
			tol := job.Spec.Template.Spec.Tolerations[i]
			// An empty key with Exists tolerates every taint, as the CNI Job's does.
			tolerated = tolerated || (tol.Operator == corev1.TolerationOpExists && (tol.Key == "" || tol.Key == taint.Key) &&
				(tol.Effect == "" || tol.Effect == taint.Effect))
		}

		if !tolerated {
			t.Errorf("%s Job tolerations = %v, want one for %s:NoSchedule", name, job.Spec.Template.Spec.Tolerations, taint.Key)
		}
	}
}

const infraPool = "infra"

// TestJobsFollowTheManagerPodScheduling pins that the installer and check Jobs take their scheduling from the
// controller's own pod, so custom manager.tolerations, nodeSelector, affinity and priorityClassName apply to them too;
// the pod is found by POD_NAME (the controller) or by its label (the CLI).
func TestJobsFollowTheManagerPodScheduling(t *testing.T) {
	mgr := &corev1.Pod{
		Namespace: trustNamespace, Name: "mgr-0", Labels: map[string]string{controlPlaneLabel: valueControllerMgr},
		Spec: corev1.PodSpec{
			Tolerations:       []corev1.Toleration{{Key: infraPool, Operator: corev1.TolerationOpExists}},
			NodeSelector:      map[string]string{"pool": infraPool},
			Affinity:          &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{}},
			PriorityClassName: "ichor-high",
			ImagePullSecrets:  []corev1.LocalObjectReference{{Name: "pull"}},
		},
	}
	r := fake.NewClientBuilder().WithObjects(mgr).Build()
	inst := &ichorv1alpha1.IchorInstallation{}

	for _, podName := range []string{"mgr-0", ""} { // by POD_NAME, then by label
		t.Setenv("POD_NAME", podName)

		for name, job := range map[string]*batchv1.Job{
			"run":   newInstallerJob(trustNamespace, "r", &ichorv1alpha1.TalosOperation{}, inst, "sha256:x", "", false),
			"check": NewInstallerCheckJob(trustNamespace, "c", inst, "sha256:x", "", false, false),
		} {
			InheritManagerScheduling(context.Background(), r, trustNamespace, job)

			if got := job.Spec.Template.Spec; len(got.Tolerations) != 1 || got.Tolerations[0].Key != infraPool ||
				got.NodeSelector["pool"] != infraPool || got.Affinity == nil || got.PriorityClassName != "ichor-high" ||
				len(got.ImagePullSecrets) != 1 {
				t.Errorf("%s Job (POD_NAME=%q) scheduling = %+v, want the manager pod's", name, podName, got)
			}
		}
	}
}
