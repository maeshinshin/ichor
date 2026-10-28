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

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/render"
)

const testKokumiPassword = "kokumi-pw-7f3a9c1e5b2d4a68"

// stackFake is a fake cluster whose controller is "slow": IchorInstallation reports StackReady
// False until it has been read stackAfter times, TalosCluster reports CNIHandover False until it
// has been read handoverAfter times. A negative count means never.
type stackFake struct {
	client.Client

	stackReads, handoverReads int
}

func newStackFake(t *testing.T, stackAfter, handoverAfter int) *stackFake {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	sf := &stackFake{}
	secret := &corev1.Secret{
		Namespace: ichorControllerNamespace, Name: kokumiAdminSecret,
		Data: map[string][]byte{kokumiAdminKey: []byte(testKokumiPassword)},
	}

	flip := func(conds *[]metav1.Condition, typ string, reads *int, after int) {
		*reads++

		status := metav1.ConditionFalse
		if after >= 0 && *reads > after {
			status = metav1.ConditionTrue
		}

		apimeta.SetStatusCondition(conds, metav1.Condition{Type: typ, Status: status, Reason: "Test", Message: "from the test controller"})
	}

	sf.Client = interceptor.NewClient(fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build(), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if err := c.Get(ctx, key, obj, opts...); err != nil {
				return err
			}

			switch o := obj.(type) {
			case *ichorv1alpha1.IchorInstallation:
				flip(&o.Status.Conditions, conditionStackReady, &sf.stackReads, stackAfter)
			case *ichorv1alpha1.TalosCluster:
				flip(&o.Status.Conditions, render.ConditionCNIHandover, &sf.handoverReads, handoverAfter)
			}

			return nil
		},
	})

	return sf
}

func releasedBuild(t *testing.T) {
	t.Helper()

	old := version.Version
	version.Version = testReleasedVersion

	t.Cleanup(func() { version.Version = old })
}

func fastWaits(deps *initDeps) {
	deps.BootstrapWait = 5 * time.Second
	deps.BootstrapInterval = time.Millisecond
	deps.StackWait = 5 * time.Second
	deps.HandoverWait = 5 * time.Second
	deps.StackInterval = time.Millisecond
}

// TestRunInit_WaitsForStackAndCNIHandover proves init creates IchorInstallation.spec.stack, then
// waits for StackReady and CNIHandover (both false at first) with progress lines, and prints the
// Kokumi admin password to stdout, once, and nowhere else.
func TestRunInit_WaitsForStackAndCNIHandover(t *testing.T) {
	withTempHome(t)
	releasedBuild(t)

	sf := newStackFake(t, 2, 3)
	deps, _, _ := newFakeInitDeps(t, sf)
	fastWaits(&deps)

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	if err := runInit(context.Background(), validInitOptions(), deps); err != nil {
		t.Fatalf("runInit: %v\nstderr:\n%s", err, stderr.String())
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := sf.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		t.Fatalf("getting IchorInstallation: %v", err)
	}

	if inst.Spec.Stack == nil || inst.Spec.Stack.Promotion != ichorv1alpha1.IchorStackPromotionAutomatic {
		t.Errorf("spec.stack = %+v, want it set with Automatic promotion", inst.Spec.Stack)
	}

	if sf.stackReads < 3 || sf.handoverReads < 4 {
		t.Errorf("init read StackReady %d and CNIHandover %d times, want it to poll until both turned True (3 and 4)", sf.stackReads, sf.handoverReads)
	}

	for _, want := range []string{
		"init: waiting for Kokumi and Argo CD (StackReady)",
		"init: waiting for Cilium to be handed over to Ichor (CNIHandover)",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks the progress line %q:\n%s", want, stderr.String())
		}
	}

	if got := strings.Count(stdout.String(), testKokumiPassword); got != 1 {
		t.Errorf("the Kokumi password is on stdout %d times, want exactly once:\n%s", got, stdout.String())
	}

	if strings.Contains(stderr.String(), testKokumiPassword) {
		t.Errorf("the Kokumi password leaked to stderr:\n%s", stderr.String())
	}
}

// TestRunInit_StackTimeoutPrintsNoPassword proves a stack that never turns Ready fails init with the
// wait's reason, and that the password is not printed for a stack that is not up.
func TestRunInit_StackTimeoutPrintsNoPassword(t *testing.T) {
	withTempHome(t)
	releasedBuild(t)

	sf := newStackFake(t, -1, 0)
	deps, _, _ := newFakeInitDeps(t, sf)
	fastWaits(&deps)

	deps.StackWait = 50 * time.Millisecond

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	err := runInit(context.Background(), validInitOptions(), deps)
	if err == nil || !strings.Contains(err.Error(), conditionStackReady) {
		t.Fatalf("runInit = %v, want an error naming %s", err, conditionStackReady)
	}

	if strings.Contains(stdout.String()+stderr.String()+err.Error(), testKokumiPassword) {
		t.Error("the Kokumi password was printed although the stack never became ready")
	}
}

// TestRunInit_HandoverTimeoutStillPrintsPasswordOnce proves the password is already out when the
// Cilium hand-over is the step that times out.
func TestRunInit_HandoverTimeoutStillPrintsPasswordOnce(t *testing.T) {
	withTempHome(t)
	releasedBuild(t)

	sf := newStackFake(t, 0, -1)
	deps, _, _ := newFakeInitDeps(t, sf)
	fastWaits(&deps)

	deps.HandoverWait = 50 * time.Millisecond

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	if err := runInit(context.Background(), validInitOptions(), deps); err == nil || !strings.Contains(err.Error(), render.ConditionCNIHandover) {
		t.Fatalf("runInit = %v, want an error naming %s", err, render.ConditionCNIHandover)
	}

	if got := strings.Count(stdout.String(), testKokumiPassword); got != 1 {
		t.Errorf("the Kokumi password is on stdout %d times, want exactly once", got)
	}
}

// TestInitCmd_RemovedFlagsAreGone proves's removed flags are rejected by cobra, and that
// --cni stays hidden and --cni-tunnel stays.
func TestInitCmd_RemovedFlagsAreGone(t *testing.T) {
	for _, name := range []string{
		trackedArgoNS, "argocd-namespace", "kokumi", "repo", "path", "repo-username", "repo-password", "gitops-dir",
	} {
		if initCmd.Flags().Lookup(name) != nil {
			t.Errorf("--%s is still defined", name)
		}

		args := []string{"--" + name}
		if name != trackedArgoNS && name != "kokumi" {
			args = []string{"--" + name + "=x"}
		}

		if err := initCmd.ParseFlags(args); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("parsing --%s = %v, want cobra's unknown flag error", name, err)
		}
	}

	if f := initCmd.Flags().Lookup("cni"); f == nil || !f.Hidden {
		t.Errorf("--cni = %+v, want it defined and hidden", f)
	}

	if initCmd.Flags().Lookup("cni-tunnel") == nil {
		t.Error("--cni-tunnel is gone")
	}
}
