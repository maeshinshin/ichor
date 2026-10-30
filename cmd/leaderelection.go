package main

import (
	"context"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Leader election timing. On a single control plane the API server is
// down for the whole static-pod restart of a CA rotation, longer than controller-runtime's
// 10 s RenewDeadline, so the default loses the lease and the manager exits. client-go requires
// leaseDuration > renewDeadline > retryPeriod*1.2 (its JitterFactor). A crash failover now
// takes up to leaseDuration.
var (
	leaseDuration = 120 * time.Second
	renewDeadline = 90 * time.Second
	retryPeriod   = 10 * time.Second
)

// newLeaseLock is the lock the manager elects with: the same Lease controller-runtime would build,
// but constructed here so the process knows its own identity and can hand the Lease over itself.
func newLeaseLock(cfg *rest.Config, namespace, name string) (resourcelock.Interface, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	cfg = rest.AddUserAgent(cfg, "leader-election")
	cfg.Timeout = max(renewDeadline/2, time.Second)

	c, err := coordinationv1client.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Namespace: namespace, Name: name},
		Client:     c,
		LockConfig: resourcelock.ResourceLockConfig{Identity: host + "_" + string(uuid.NewUUID())},
	}, nil
}

// releaseLeaseUnlessRestoring hands the Lease over after a plain voluntary stop (an upgrade, a
// rollout), so the next controller pod leads at once instead of after leaseDuration. It does nothing
// when the process goes on into the etcd restore tail: it must keep the lease then. The Lease is only cleared while
// this process still holds it, with an update that
// fails if anyone changed it since it was read.
func releaseLeaseUnlessRestoring(ctx context.Context, lock resourcelock.Interface, restoring func() bool) error {
	if restoring() {
		return nil
	}

	rec, _, err := lock.Get(ctx)
	if err != nil || rec.HolderIdentity != lock.Identity() {
		return err
	}

	now := metav1.NewTime(time.Now())
	rec.HolderIdentity, rec.LeaseDurationSeconds, rec.RenewTime = "", 1, now

	return lock.Update(ctx, *rec)
}
