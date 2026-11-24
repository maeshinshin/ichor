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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
)

// targetWithUI is a target manifest: the two Deployments the installer customizes, and a CRD with props.
func targetWithUI(props ...string) []byte {
	dep := func(name, container string) string {
		return "---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + ", namespace: " + ichorControllerNamespace + "}\nspec:\n  selector:\n    matchLabels: {app: " + name + "}\n" +
			"  template:\n    metadata:\n      labels: {app: " + name + "}\n    spec:\n      containers:\n      - name: " + container + "\n        image: x\n        args: [--addr=:8080]\n"
	}

	return []byte(widgetCRD(props...) + dep(testPrefixedUIDeployment, uiContainer) + dep(testControllerDeployment, controllerContainer))
}

// signInOffCluster is an install whose ichor-ui has no sign-in, with a live CRD of the given properties. writes counts
// every write but the check Job's own create and delete; the Job's pod is played by installer.Check, run with the
// flags the Job was given.
func signInOffCluster(t *testing.T, target []byte, liveProps []string, writes *int) client.Client {
	t.Helper()

	live, err := installer.Decode([]byte(widgetCRD(liveProps...)))
	if err != nil {
		t.Fatal(err)
	}

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: runningVersion, InstallerRepository: checkTestRepo}}
	inst.Name = ichorv1alpha1.IchorInstallationName
	cluster := newTestCluster()
	cluster.Namespace, cluster.Spec.VersionContract = ichorControllerNamespace, testVersionContract

	count := func() { *writes++ }

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(slices.Concat(uiObjects(), []client.Object{cluster, inst, live[0]})...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
				job, ok := o.(*batchv1.Job)
				if !ok {
					count()

					return c.Create(ctx, o, opts...)
				}

				args := job.Spec.Template.Spec.Containers[0].Args
				checkErr := installer.Check(ctx, c, installer.Options{Namespace: job.Namespace, Manifest: target, SignInPending: slices.Contains(args, "--sign-in-pending")})

				if err := c.Create(ctx, job); err != nil {
					return err
				}

				cond := batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}

				if checkErr != nil {
					cond = batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}

					pod := &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Message: checkErr.Error(), FinishedAt: metav1.Now()}}}}}}
					pod.Namespace, pod.Name, pod.Labels = job.Namespace, job.Name+"-pod", map[string]string{"batch.kubernetes.io/job-name": job.Name}

					if err := c.Create(ctx, pod); err != nil {
						return err
					}
				}

				job.Status.Conditions = []batchv1.JobCondition{cond}

				return c.Status().Update(ctx, job)
			},
			Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
				count()

				return c.Update(ctx, o, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
				count()

				return c.Patch(ctx, o, p, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
				if _, ok := o.(*batchv1.Job); !ok {
					count()
				}

				return c.Delete(ctx, o, opts...)
			},
		}).Build()
}

func upgradeAgainstSignInOff(t *testing.T, target []byte, liveProps []string) (writes int, started bool, signedInAtStart bool, err error) {
	t.Helper()

	installerCheckPoll, installerCheckWait = time.Millisecond, 5*time.Second

	k := signInOffCluster(t, target, liveProps, &writes)

	err = upgradeWithSignIn(context.Background(), k, strings.NewReader(""), io.Discard, initOptions{}, true,
		func() error {
			pending, err := signInIsPending(context.Background(), k, initOptions{})
			if err != nil {
				return err
			}

			_, err = runInstallerCheck(context.Background(), k, fakeImages{}, io.Discard, "v0.3.0", "", false, pending)

			return err
		},
		func() error {
			started = true
			signedInAtStart = slices.Contains(uiDeploymentArgs(t, k), argUsersEnabled)

			return nil
		})

	return writes, started, signedInAtStart, err
}

// The command that turns sign-in on is not refused for the sign-in it is about to set up: the check passes, then
// ensureSignIn runs, then the upgrade starts.
func TestUpgradeIchorWithSignInOffPassesTheCheckThenSignsIn(t *testing.T) {
	_, started, signedIn, err := upgradeAgainstSignInOff(t, targetWithUI("size"), []string{"size"})
	if err != nil {
		t.Fatalf("the upgrade of a sign-in-less install was refused: %v", err)
	}

	if !started || !signedIn {
		t.Errorf("started=%v, sign-in on at start=%v: want the upgrade to start after sign-in is on", started, signedIn)
	}
}

// A refusal for another reason (a CRD that would lose a property) still comes first: sign-in stays off, no write.
func TestUpgradeIchorWithSignInOffStillRefusesForOtherReasons(t *testing.T) {
	writes, started, _, err := upgradeAgainstSignInOff(t, targetWithUI("size"), []string{"color", "size"})
	if err == nil || !strings.Contains(err.Error(), "color would be removed") {
		t.Fatalf("want the CRD refusal, got %v", err)
	}

	if started || writes != 0 {
		t.Errorf("a write came before the refusal: started=%v writes=%d", started, writes)
	}
}

// The check Job is told sign-in is pending exactly when ensureSignIn is going to act.
func TestSignInIsPendingExactlyWhenEnsureSignInWillAct(t *testing.T) {
	off := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).Build()

	on := uiObjects()
	ui := on[0].(*appsv1.Deployment)
	ui.Spec.Template.Spec.Containers[0].Args = append(ui.Spec.Template.Spec.Containers[0].Args, argUsersEnabled, "--external-url=https://ui.example")
	signedIn := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(on...).Build()

	for name, tc := range map[string]struct {
		k    client.Client
		want bool
	}{"sign-in off": {off, true}, "sign-in on": {signedIn, false}} {
		got, err := signInIsPending(context.Background(), tc.k, initOptions{})
		if err != nil || got != tc.want {
			t.Errorf("%s: pending=%v err=%v, want %v", name, got, err, tc.want)
		}
	}
}
