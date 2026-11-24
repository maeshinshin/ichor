package cli

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/imagesig"
	"github.com/maeshinshin/ichor/internal/installer"
)

// runningVersion is the version the upgrade tests start from.
const runningVersion = "v0.2.4"

type fakeImages struct{}

func (fakeImages) Resolve(context.Context, string, string) (string, error) {
	return "sha256:" + strings.Repeat("a", 64), nil
}
func (fakeImages) Verify(context.Context, imagesig.Image, imagesig.Identity) error { return nil }

func widgetCRD(props ...string) string {
	p := ""
	for _, n := range props {
		p += "                " + n + ": {type: string}\n"
	}

	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.dev}
spec:
  group: example.dev
  scope: Namespaced
  names: {kind: Widget, plural: widgets}
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
` + p
}

// TestUpgradeIchorInstallerCheckRefusesBeforeSignIn: the target's installer image refuses (its CRD would remove a
// property of the live one) in the check Job, and by then nothing has been written but that Job: sign-in is still off.
// The Job's pod is played by the in-process installer.Check, the function `installer check` runs.
func TestUpgradeIchorInstallerCheckRefusesBeforeSignIn(t *testing.T) {
	installerCheckPoll, installerCheckWait = time.Millisecond, 5*time.Second

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	scheme.AddKnownTypes(ichorv1alpha1.GroupVersion, &ichorv1alpha1.IchorInstallation{}, &ichorv1alpha1.IchorInstallationList{}, &ichorv1alpha1.TalosCluster{}, &ichorv1alpha1.TalosClusterList{}, &ichorv1alpha1.IchorUser{}, &ichorv1alpha1.IchorUserList{})
	metav1.AddToGroupVersion(scheme, ichorv1alpha1.GroupVersion)

	live, err := installer.Decode([]byte(widgetCRD("color", "size")))
	if err != nil {
		t.Fatal(err)
	}

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: runningVersion, InstallerRepository: "registry.example/ichor"}}
	inst.Name = ichorv1alpha1.IchorInstallationName
	cluster := newTestCluster()
	cluster.Namespace, cluster.Spec.VersionContract = ichorControllerNamespace, testVersionContract

	var writes, jobCreates, jobDeletes int

	var runs []string

	target := []byte(widgetCRD("size")) // drops "color"

	k := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(slices.Concat(uiObjects(), []client.Object{cluster, inst, live[0]})...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
				job, ok := o.(*batchv1.Job)
				if !ok {
					writes++

					return c.Create(ctx, o, opts...)
				}

				jobCreates++
				runs = append(runs, strings.Join(job.Spec.Template.Spec.Containers[0].Args, " "))

				// The pod: run the installer's check against the cluster and record what it would terminate with.
				checkErr := installer.Check(ctx, c, installer.Options{Namespace: job.Namespace, Manifest: target})
				if err := c.Create(ctx, job); err != nil {
					return err
				}

				cond := batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}

				if checkErr != nil {
					cond = batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}

					pod := &corev1.Pod{
						Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{Message: checkErr.Error(), FinishedAt: metav1.Now()}}}}},
					}
					pod.Namespace, pod.Name, pod.Labels = job.Namespace, job.Name+"-pod", map[string]string{"batch.kubernetes.io/job-name": job.Name}

					if err := c.Create(ctx, pod); err != nil {
						return err
					}
				}

				job.Status.Conditions = []batchv1.JobCondition{cond}

				return c.Status().Update(ctx, job)
			},
			Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
				writes++

				return c.Update(ctx, o, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
				writes++

				return c.Patch(ctx, o, p, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
				if _, ok := o.(*batchv1.Job); ok {
					jobDeletes++
				} else {
					writes++
				}

				return c.Delete(ctx, o, opts...)
			},
			SubResource: func(c client.WithWatch, s string) client.SubResourceClient { writes++; return c.SubResource(s) },
		}).Build()

	started := false
	err = upgradeWithSignIn(context.Background(), k, strings.NewReader(""), io.Discard, initOptions{}, true,
		func() error {
			return runInstallerCheck(context.Background(), k, fakeImages{}, io.Discard, "v0.3.0", "", false)
		},
		func() error { started = true; return nil })

	if err == nil || !strings.Contains(err.Error(), "color would be removed") {
		t.Fatalf("want the installer's refusal verbatim (color would be removed), got %v", err)
	}

	if started || writes != 0 {
		t.Errorf("a write came before the refusal: started=%v writes=%d", started, writes)
	}

	if jobCreates != 1 || jobDeletes != 1 {
		t.Errorf("check Job creates=%d deletes=%d, want 1 and 1", jobCreates, jobDeletes)
	}

	if len(runs) != 1 || !strings.HasPrefix(runs[0], "installer check ") {
		t.Errorf("the Job ran %v, want `installer check`", runs)
	}

	var dep appsv1.Deployment
	if err := k.Get(context.Background(), client.ObjectKey{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	if slices.Contains(dep.Spec.Template.Spec.Containers[0].Args, argUsersEnabled) {
		t.Error("sign-in was switched on")
	}
}
