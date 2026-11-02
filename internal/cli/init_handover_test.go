package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A hand-over that times out on a cluster with tainted control planes says that a worker has to be
// approved: Cilium's cluster-wide parts can wait for one.
func TestWaitForCiliumHandover_TimeoutHintsAtWorkers(t *testing.T) {
	for schedulable, wantHint := range map[string]bool{schedulableFalse: true, schedulableTrue: false} {
		cluster := &ichorv1alpha1.TalosCluster{}
		cluster.Name, cluster.Namespace = "c", "ns"
		cluster.Spec.ControlPlane.Schedulable = schedulable

		c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()
		deps := initDeps{Stderr: &bytes.Buffer{}, HandoverWait: 50 * time.Millisecond, HandoverInterval: 10 * time.Millisecond}

		err := waitForCiliumHandover(t.Context(), c, true, cluster, deps)
		if err == nil {
			t.Fatal("no error from a hand-over that never completes")
		}

		if got := strings.Contains(err.Error(), "approve a worker"); got != wantHint {
			t.Errorf("schedulable=%s: hint present = %t, want %t: %v", schedulable, got, wantHint, err)
		}
	}
}
