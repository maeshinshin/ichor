package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// checkTestRepo is the installer repository the check tests use.
const checkTestRepo = "registry.example/ichor"

type failingImages struct{ fakeImages }

func (failingImages) Resolve(context.Context, string, string) (string, error) {
	return "", errors.New("dial tcp: no route to host")
}

func checkClient(t *testing.T, complete bool) client.Client {
	t.Helper()

	scheme := newTestScheme(t)

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: runningVersion, InstallerRepository: checkTestRepo}}
	inst.Name = ichorv1alpha1.IchorInstallationName

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if job, ok := o.(*batchv1.Job); ok && complete {
				job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
			}

			return c.Create(ctx, o, opts...)
		},
	}).Build()
}

// The digest the check pinned and verified is returned, and is the pin the upgrade operation is created with.
func TestUpgradeIchorOperationCarriesTheCheckedDigest(t *testing.T) {
	installerCheckPoll, installerCheckWait = time.Millisecond, 5*time.Second

	checked, err := runInstallerCheck(context.Background(), checkClient(t, true), fakeImages{}, io.Discard, "v0.3.0", "", false, false)
	if err != nil {
		t.Fatal(err)
	}

	if want := "sha256:" + strings.Repeat("a", 64); checked != want {
		t.Fatalf("the check returned digest %q, want the resolved %q", checked, want)
	}

	// No --installer-digest was given: the operation still pins the checked one.
	o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_ICHOR, checked)
	if o.GetDigest() != checked {
		t.Errorf("the operation's installer digest is %q, want the checked %q", o.GetDigest(), checked)
	}
}

// A registry the CLI cannot read stops the upgrade before any Job, and the error says what to do.
func TestUpgradeIchorUnreachableRegistryNamesTheWayOut(t *testing.T) {
	_, err := runInstallerCheck(context.Background(), checkClient(t, false), failingImages{}, io.Discard, "v0.3.0", "", false, false)
	if err == nil {
		t.Fatal("no error")
	}

	for _, want := range []string{"from this machine", "run it from a host that reaches " + checkTestRepo, "--installer-digest sha256:... --skip-signature-check", "no route to host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

// With --ichor-namespace other the check Job runs in other, and its timeout message names that namespace.
func TestUpgradeIchorCheckJobRunsInTheEffectiveControllerNamespace(t *testing.T) {
	old, oldPoll, oldWait := apiOpts.IchorNamespace, installerCheckPoll, installerCheckWait
	apiOpts.IchorNamespace, installerCheckPoll, installerCheckWait = testOtherNS, time.Millisecond, 20*time.Millisecond

	t.Cleanup(func() { apiOpts.IchorNamespace, installerCheckPoll, installerCheckWait = old, oldPoll, oldWait })

	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: runningVersion, InstallerRepository: checkTestRepo}}
	inst.Name = ichorv1alpha1.IchorInstallationName

	var jobNS string

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(inst).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if job, ok := o.(*batchv1.Job); ok {
				jobNS = job.Namespace
			}

			return c.Create(ctx, o, opts...)
		},
	}).Build()

	_, err := runInstallerCheck(context.Background(), k, fakeImages{}, io.Discard, "v0.3.0", "", false, false)

	if jobNS != testOtherNS {
		t.Errorf("the check Job was created in %q, want %q", jobNS, testOtherNS)
	}

	if err == nil || !strings.Contains(err.Error(), "pod in "+testOtherNS) {
		t.Errorf("the timeout message %v does not name %s", err, testOtherNS)
	}
}
