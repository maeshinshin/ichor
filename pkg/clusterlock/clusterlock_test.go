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

package clusterlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// These exercise real optimistic-concurrency and conflict behaviour against
// a real kube-apiserver (envtest), not a fake client: the semantics under
// test are entirely about concurrent writes, which a fake client would let
// through without ever surfacing a conflict.

// Names reused across test cases below.
const (
	clusterName = "demo"
	identityA   = "holder-a"
	identityB   = "holder-b"
)

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
)

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting envtest:", err)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, "building scheme:", err)
		os.Exit(1)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, "building client:", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "stopping envtest:", err)
	}
	os.Exit(code)
}

// newTestNamespace creates a fresh namespace so tests can run with
// independent state without tearing anything down themselves.
func newTestNamespace(t *testing.T) string {
	t.Helper()

	ns := &corev1.Namespace{
		GenerateName: "clusterlock-test-",
	}
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		t.Fatalf("creating test namespace: %v", err)
	}
	return ns.Name
}

// newTestLock returns a context and a Lock on a fresh namespace's "demo" cluster.
func newTestLock(t *testing.T, d time.Duration) (context.Context, *Lock) {
	t.Helper()

	return t.Context(), New(k8sClient, types.NamespacedName{Namespace: newTestNamespace(t), Name: clusterName}, d)
}

// wantHolder fails unless lock is held by want.
func wantHolder(t *testing.T, lock *Lock, want string) {
	t.Helper()

	if holder, held, err := lock.Holder(t.Context()); err != nil || !held || holder != want {
		t.Fatalf("Holder() = (%q, %v, %v), want (%q, true, nil)", holder, held, err, want)
	}
}

func TestAcquire_SecondFailsWhileFirstHolds(t *testing.T) {
	ctx, lock := newTestLock(t, time.Minute)

	if err := lock.Acquire(ctx, identityA); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	err := lock.Acquire(ctx, identityB)
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("second Acquire = %v, want *HeldError", err)
	}
	if held.Holder != identityA {
		t.Fatalf("second Acquire reported holder %q, want holder-a", held.Holder)
	}
}

func TestAcquire_DifferentClustersBothSucceed(t *testing.T) {
	ns := newTestNamespace(t)
	ctx := context.Background()
	a := New(k8sClient, types.NamespacedName{Namespace: ns, Name: "cluster-a"}, time.Minute)
	b := New(k8sClient, types.NamespacedName{Namespace: ns, Name: "cluster-b"}, time.Minute)

	if err := a.Acquire(ctx, identityA); err != nil {
		t.Fatalf("Acquire cluster-a: %v", err)
	}
	if err := b.Acquire(ctx, identityB); err != nil {
		t.Fatalf("Acquire cluster-b: %v", err)
	}
}

func TestAcquire_ExpiredLeaseIsStolenByDifferentHolder(t *testing.T) {
	ctx, lock := newTestLock(t, time.Second)

	if err := lock.Acquire(ctx, identityA); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	time.Sleep(1200 * time.Millisecond) // past the 1s duration, no renewal

	if err := lock.Acquire(ctx, identityB); err != nil {
		t.Fatalf("Acquire of an expired lease by a different holder: %v", err)
	}

	wantHolder(t, lock, identityB)
}

func TestRenew_KeepsHeldPastOriginalExpiry(t *testing.T) {
	ctx, lock := newTestLock(t, time.Second)

	if err := lock.Acquire(ctx, identityA); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	time.Sleep(500 * time.Millisecond) // before the original expiry
	if err := lock.Renew(ctx, identityA); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	// Total elapsed since Acquire is now past the original 1s duration, but
	// only ~700ms past the renewal, so the lock must still be held.
	time.Sleep(700 * time.Millisecond)

	err := lock.Acquire(ctx, identityB)
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("Acquire after renewal, past original expiry = %v, want *HeldError", err)
	}
	if held.Holder != identityA {
		t.Fatalf("still-held Acquire reported holder %q, want holder-a", held.Holder)
	}
}

func TestRelease_ByHolderFreesIt(t *testing.T) {
	ctx, lock := newTestLock(t, time.Minute)

	if err := lock.Acquire(ctx, identityA); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(ctx, identityA); err != nil {
		t.Fatalf("Release by the holder: %v", err)
	}
	if err := lock.Acquire(ctx, identityB); err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
}

func TestRelease_ByNonHolderIsNoop(t *testing.T) {
	ctx, lock := newTestLock(t, time.Minute)

	if err := lock.Acquire(ctx, identityA); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Releasing with the wrong identity must not error destructively and
	// must not free holder-a's lock: a reconciler that errored halfway
	// through, and does not know (or does not trust) who holds the lock,
	// has to be able to call this defensively.
	if err := lock.Release(ctx, identityB); err != nil {
		t.Fatalf("Release by a non-holder returned an error: %v, want nil", err)
	}

	wantHolder(t, lock, identityA) // not freed
}

func TestAcquire_SameHolderResumesAfterRestart(t *testing.T) {
	ctx, lock := newTestLock(t, time.Minute)

	if err := lock.Acquire(ctx, "ichor-controller"); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	var before coordinationv1.Lease
	if err := k8sClient.Get(ctx, lock.key, &before); err != nil {
		t.Fatalf("Get before resumption: %v", err)
	}

	// Simulate a restart or a leader change: a new process, holding no
	// in-memory state from the first Acquire, but configured with the same
	// stable identity, resumes work on the same cluster.
	resumed := New(k8sClient, lock.key, time.Minute)
	if err := resumed.Acquire(ctx, "ichor-controller"); err != nil {
		t.Fatalf("resuming Acquire with the same identity: %v", err)
	}

	var after coordinationv1.Lease
	if err := k8sClient.Get(ctx, lock.key, &after); err != nil {
		t.Fatalf("Get after resumption: %v", err)
	}
	if *after.Spec.LeaseTransitions != *before.Spec.LeaseTransitions {
		t.Fatalf("leaseTransitions changed from %d to %d on same-identity resumption, want unchanged",
			*before.Spec.LeaseTransitions, *after.Spec.LeaseTransitions)
	}
	if !after.Spec.AcquireTime.Equal(before.Spec.AcquireTime) {
		t.Fatalf("acquireTime changed on same-identity resumption: %v -> %v", before.Spec.AcquireTime, after.Spec.AcquireTime)
	}
}

func TestAcquire_RaceProducesExactlyOneWinner(t *testing.T) {
	ctx, lock := newTestLock(t, time.Minute)

	const racers = 20
	var wins atomic.Int32
	var wg sync.WaitGroup
	wg.Add(racers)
	for i := range racers {
		go func(i int) {
			defer wg.Done()
			if err := lock.Acquire(ctx, fmt.Sprintf("racer-%d", i)); err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("%d of %d racing Acquire calls succeeded, want exactly 1", got, racers)
	}
}
