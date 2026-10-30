package main

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func leaseLockFor(t *testing.T, identity string) *resourcelock.LeaseLock {
	t.Helper()

	return &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Namespace: "ns", Name: "lock"},
		Client:     fake.NewClientset().CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: identity},
	}
}

// A voluntary stop (an upgrade, a rollout) hands the Lease over, so the next controller pod does not
// wait out leaseDuration before it issues the certificates ichor-ui needs.
func TestVoluntaryStopReleasesTheLease(t *testing.T) {
	ctx := context.Background()
	lock := leaseLockFor(t, "me")

	if err := lock.Create(ctx, resourcelock.LeaderElectionRecord{HolderIdentity: "me", LeaseDurationSeconds: 120}); err != nil {
		t.Fatal(err)
	}

	if released, err := releaseLeaseUnlessRestoring(ctx, lock, func() bool { return false }); err != nil || !released {
		t.Fatalf("released %v, %v", released, err)
	}

	rec, _, err := lock.Get(ctx)
	if err != nil || rec.HolderIdentity != "" {
		t.Fatalf("lease still held: %+v %v", rec, err)
	}
}

// Going into the etcd restore tail the lease is kept, and a lease someone else holds
// is never touched.
func TestLeaseIsKeptInTheRestoreTailAndWhenHeldByAnother(t *testing.T) {
	ctx := context.Background()

	for name, tc := range map[string]struct {
		holder    string
		restoring bool
	}{"restore tail": {"me", true}, "held by another": {"other", false}} {
		t.Run(name, func(t *testing.T) {
			lock := leaseLockFor(t, "me")
			if err := lock.Create(ctx, resourcelock.LeaderElectionRecord{HolderIdentity: tc.holder, LeaseDurationSeconds: 120}); err != nil {
				t.Fatal(err)
			}

			if released, err := releaseLeaseUnlessRestoring(ctx, lock, func() bool { return tc.restoring }); err != nil || released {
				t.Fatalf("released %v, %v", released, err)
			}

			if rec, _, err := lock.Get(ctx); err != nil || rec.HolderIdentity != tc.holder {
				t.Fatalf("lease changed: %+v %v", rec, err)
			}
		})
	}
}

// The client-go constraint, checked by constructing the elector itself.
func TestLeaderElectionDurationsAreValid(t *testing.T) {
	if renewDeadline < 60*time.Second {
		t.Fatalf("renewDeadline %s does not outlive a 60 s API outage", renewDeadline)
	}

	_, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          &resourcelock.LeaseLock{LockConfig: resourcelock.ResourceLockConfig{Identity: "test"}},
		LeaseDuration: leaseDuration, RenewDeadline: renewDeadline, RetryPeriod: retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(context.Context) {}, OnStoppedLeading: func() {},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}
