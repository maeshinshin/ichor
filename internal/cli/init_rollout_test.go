package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// fakeController answers what the controller would: Cilium handed over, the first machine
// waiting for an API server restart, and every config operation completed. It stops with the test.
func fakeController(t *testing.T, c client.Client) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		for ctx.Err() == nil {
			time.Sleep(5 * time.Millisecond)

			var clusters ichorv1alpha1.TalosClusterList
			if c.List(ctx, &clusters) == nil {
				for i := range clusters.Items {
					cl := &clusters.Items[i]
					cl.Status.Conditions = []metav1.Condition{{Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: "Done", LastTransitionTime: metav1.Now()}}
					_ = c.Status().Update(ctx, cl)
				}
			}

			var machines ichorv1alpha1.TalosMachineList
			if c.List(ctx, &machines) == nil {
				for i := range machines.Items {
					m := &machines.Items[i]
					m.Status.APIServerRestartPaths = []string{"KubeAuthenticationConfig"}
					_ = c.Status().Update(ctx, m)
				}
			}

			var ops ichorv1alpha1.TalosOperationList
			if c.List(ctx, &ops) == nil {
				for i := range ops.Items {
					ops.Items[i].Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
					_ = c.Status().Update(ctx, &ops.Items[i])
				}
			}
		}
	}()
}

// A new cluster with sign-in ends Ready: init does the config rollout the authentication needs
// instead of leaving the control plane at APIServerRestartRequired.
func TestRunInit_RollsOutTheAuthenticationConfig(t *testing.T) {
	withTempHome(t)
	setVersion(t, "v0.3.1")
	shrinkInitRetry(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithStatusSubresource(&ichorv1alpha1.TalosCluster{}, &ichorv1alpha1.TalosMachine{}, &ichorv1alpha1.TalosOperation{}).Build()
	deps, _, _ := newFakeInitDeps(t, k8sClient)
	deps.WebhookWait, deps.HandoverWait, deps.HandoverInterval = 2*time.Second, 5*time.Second, 5*time.Millisecond
	deps.BootstrapInterval = 5 * time.Millisecond

	fakeController(t, k8sClient)

	opts := validInitOptions()
	opts.InstallerDigest = "sha256:" + strings.Repeat("a", 64)
	opts.SkipSignatureCheck = true

	if err := runInit(t.Context(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := k8sClient.List(t.Context(), &ops); err != nil {
		t.Fatal(err)
	}

	if len(ops.Items) != 1 || ops.Items[0].Spec.Kind != ichorv1alpha1.TalosOperationKindConfig {
		t.Fatalf("operations = %+v, want one config operation", ops.Items)
	}

	if out := deps.Stderr.(interface{ String() string }).String(); strings.Contains(out, "until you run `ichor rollout config`") {
		t.Errorf("init still tells the operator to run the rollout:\n%s", out)
	}
}
