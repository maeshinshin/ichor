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

package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/kubetrust"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	trustNamespace  = "ichor-system"
	trustBundleName = "rot-bundle"
)

// applyTrustBase makes pool read base as the projected in-cluster ca.crt.
func applyTrustBase(pool *kubetrust.Pool, base string) {
	pool.Apply(&rest.Config{
		Host: "https://127.0.0.1:6443", BearerToken: "x", CAFile: base,
	})
}

// A replica that does not run the rotation must still trust the new Kubernetes CA while a rotation is
// under way and drop it once it is over and the projected ca.crt carries it.
// rotatingTrustClient is a fake client holding a cluster, its rotation bundle Secret and a ca-rotation
// operation, in progress (Issuing) when inProgress; it returns the rotation and the operation too.
func rotatingTrustClient(t *testing.T, inProgress bool) (client.Client, *secretsbundle.Rotation, *ichorv1alpha1.TalosOperation) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	contract, err := coreconfig.ParseContractFromVersion("v1.14")
	if err != nil {
		t.Fatal(err)
	}

	rot, err := secretsbundle.NewRotation(time.Now(), contract)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := rot.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	cluster := &ichorv1alpha1.TalosCluster{Namespace: testIchorNamespace, Name: "c"}
	cluster.Spec.SecretsRef.Name = trustBundleName
	secret := &corev1.Secret{
		Namespace: trustNamespace, Name: trustBundleName,
		Data: map[string][]byte{secretsbundle.RotationKey: raw},
	}
	op := &ichorv1alpha1.TalosOperation{Namespace: testIchorNamespace, Name: "rot"}
	op.Spec.ClusterRef.Name, op.Spec.Kind = "c", ichorv1alpha1.TalosOperationKindCARotation

	if inProgress {
		op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{Stage: ichorv1alpha1.CARotationStageIssuing}
		op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, secret, op).Build(), rot, op
}

func TestCATrustSyncFollowsARotationOnAnyReplica(t *testing.T) {
	const ns = trustNamespace

	cl, rot, op := rotatingTrustClient(t, true)

	dir := t.TempDir()
	base, file := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "trust", "kubernetes-ca.crt")

	if err := os.WriteFile(base, []byte("not the new CA\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pool := &kubetrust.Pool{}
	applyTrustBase(pool, base)

	sync := &CATrustSync{Reader: cl, ControllerNamespace: ns, Pool: pool, File: file}
	if sync.NeedLeaderElection() {
		t.Fatal("the trust sync must run on every replica")
	}

	read := func() string { b, _ := os.ReadFile(file); return string(b) }

	sync.syncOnce(context.Background())

	if !strings.Contains(read(), strings.TrimSpace(string(rot.Kubernetes.Crt))) || strings.Contains(read(), "PRIVATE KEY") {
		t.Fatalf("the follower did not publish the new CA's certificate:\n%s", read())
	}

	// Finished, but the projected ca.crt still lags: the extras stay.
	op.Status.CARotation.Stage, op.Status.Phase = ichorv1alpha1.CARotationStageDone, ichorv1alpha1.TalosOperationPhaseCompleted
	if err := cl.Update(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	sync.syncOnce(context.Background())

	if read() == "" {
		t.Fatal("cleared before the projected ca.crt carried the new CA")
	}

	// A restarted container reads the file back, and clears on its first pass once ca.crt has caught up.
	restarted := &kubetrust.Pool{}
	applyTrustBase(restarted, base)

	if err := restarted.LoadFile(file); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(base, rot.Kubernetes.Crt, 0o600); err != nil {
		t.Fatal(err)
	}

	(&CATrustSync{Reader: cl, ControllerNamespace: ns, Pool: restarted, File: file}).syncOnce(context.Background())

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the restarted replica did not clear the trust file: %v", err)
	}
}

// fakeInformers hands CATrustSync's handler back to the test.
type fakeInformers struct {
	cache.Informer
	registered chan toolscache.ResourceEventHandler
}

func (f *fakeInformers) GetInformer(context.Context, client.Object, ...cache.InformerGetOption) (cache.Informer, error) {
	return f, nil
}

func (f *fakeInformers) AddEventHandler(h toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	f.registered <- h

	return nil, nil
}

// A rotation that appears must be published at once, not at the next tick: Accepting to Issuing
// takes only a few seconds.
func TestCATrustSyncPublishesOnAnOperationEventWithoutWaitingForATick(t *testing.T) {
	cl, rot, op := rotatingTrustClient(t, false)

	dir := t.TempDir()
	file := filepath.Join(dir, "trust", "kubernetes-ca.crt")
	inf := &fakeInformers{registered: make(chan toolscache.ResourceEventHandler, 1)}
	// An hour-long tick: only the event can make the pass happen.
	sync := &CATrustSync{
		Reader: cl, ControllerNamespace: trustNamespace, Pool: &kubetrust.Pool{}, File: file,
		Interval: time.Hour, Informers: inf,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { _ = sync.Start(ctx); close(done) }()

	defer func() { cancel(); <-done }()

	var handler toolscache.ResourceEventHandler

	select {
	case handler = <-inf.registered:
	case <-time.After(5 * time.Second):
		t.Fatal("no event handler was registered")
	}

	// Not under way yet (as at creation, before the status is set): no trigger, nothing published.
	handler.OnAdd(op, false)

	// The leader saves rotation.yaml, then sets Accepting: that status update is the event.
	op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{Stage: ichorv1alpha1.CARotationStageAccepting}
	op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress

	if err := cl.Update(context.Background(), op); err != nil {
		t.Fatal(err)
	}

	handler.OnUpdate(nil, op)

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(file); strings.Contains(string(b), strings.TrimSpace(string(rot.Kubernetes.Crt))) {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("the new CA was not published after the operation event")
}
