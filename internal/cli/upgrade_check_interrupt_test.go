package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Ctrl-C while the check Job runs ends the wait and still deletes the Job: the process would otherwise die with the
// Job (and its pod) left in the namespace.
func TestUpgradeIchorInstallerCheckInterruptDeletesTheJob(t *testing.T) {
	installerCheckPoll, installerCheckWait = time.Millisecond, 30*time.Second

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	scheme.AddKnownTypes(ichorv1alpha1.GroupVersion, &ichorv1alpha1.IchorInstallation{}, &ichorv1alpha1.IchorInstallationList{})
	metav1.AddToGroupVersion(scheme, ichorv1alpha1.GroupVersion)

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: runningVersion, InstallerRepository: "registry.example/ichor"}}
	inst.Name = ichorv1alpha1.IchorInstallationName

	k := fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if _, ok := o.(*batchv1.Job); ok {
				go func() {
					time.Sleep(100 * time.Millisecond)

					_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
				}()
			}

			return c.Create(ctx, o, opts...)
		},
	}).Build()

	err := runInstallerCheck(context.Background(), k, fakeImages{}, io.Discard, "v0.3.0", "", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want the wait to end with context.Canceled, got %v", err)
	}

	var jobs batchv1.JobList
	if err := k.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}

	if len(jobs.Items) != 0 {
		t.Errorf("the check Job was left behind: %d Jobs", len(jobs.Items))
	}

}
