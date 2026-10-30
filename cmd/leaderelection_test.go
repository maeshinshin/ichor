package main

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// A voluntary stop (an upgrade, a rollout) must hand the lease over: otherwise the next controller
// pod waits out leaseDuration before it issues anything, and ichor-ui, which needs the certificates
// that controller issues, stays unready for about two minutes.
func TestVoluntaryStopReleasesTheLease(t *testing.T) {
	if !releaseLeaseOnCancel {
		t.Fatal("releaseLeaseOnCancel is off: a restart waits out the whole lease")
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
