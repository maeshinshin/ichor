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
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const uiTestNamespace = testNamespace

// uiTestPodA and uiTestPodB are the two pod-name fixtures every
// two-pod test below shares.
const (
	uiTestPodA = "pod-a"
	uiTestPodB = "pod-b"
)

var uiTestSelector = map[string]string{"app": "ichor-ui"}

// uiTestPod builds a Running, Ready TalosMachine-independent Pod fixture
// -- ui.go never looks at anything machine-shaped, only corev1.Pod/Service
// -- with uiTestSelector's labels, so it is always what the Service
// fixture below would actually select.
func uiTestPod(name string) *corev1.Pod {
	return &corev1.Pod{
		Name: name, Namespace: uiTestNamespace, Labels: uiTestSelector,
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func uiTestService() *corev1.Service {
	return &corev1.Service{
		Name: testUIServiceName, Namespace: uiTestNamespace, Labels: map[string]string{uiComponentLabel: uiComponentValue},
		Spec: corev1.ServiceSpec{
			Ports:    []corev1.ServicePort{{Port: 8080}},
			Selector: uiTestSelector,
		},
	}
}

// TestSelectPod_PrefersNonExcluded proves the re-forward decision: given
// two ready pods, excluding the one that just failed picks the other one
// deterministically (sorted by name), not whichever the API happened to
// list first.
func TestSelectPod_PrefersNonExcluded(t *testing.T) {
	pods := []corev1.Pod{*uiTestPod(uiTestPodB), *uiTestPod(uiTestPodA)}

	got, ok := selectPod(pods, uiTestPodA)
	if !ok {
		t.Fatal("selectPod: expected a pod, got none")
	}

	if got.Name != uiTestPodB {
		t.Fatalf("selectPod excluding pod-a = %q, want pod-b", got.Name)
	}
}

// TestSelectPod_FallsBackToExcludedWhenOnlyOption proves a single-replica
// cluster still gets a target: excluding the only ready pod must not
// leave selectPod with nothing to return.
func TestSelectPod_FallsBackToExcludedWhenOnlyOption(t *testing.T) {
	pods := []corev1.Pod{*uiTestPod(uiTestPodA)}

	got, ok := selectPod(pods, uiTestPodA)
	if !ok || got.Name != uiTestPodA {
		t.Fatalf("selectPod = (%v, %v), want (pod-a, true)", got, ok)
	}
}

// TestSelectPod_IgnoresNotReadyAndTerminating proves a pod mid-rollout
// (not Ready yet) or mid-drain (DeletionTimestamp set) is never a
// forwarding target.
func TestSelectPod_IgnoresNotReadyAndTerminating(t *testing.T) {
	notReady := uiTestPod("pod-not-ready")
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse

	terminating := uiTestPod("pod-terminating")
	now := metav1.Now()
	terminating.DeletionTimestamp = &now

	ready := uiTestPod("pod-ready")

	got, ok := selectPod([]corev1.Pod{*notReady, *terminating, *ready}, "")
	if !ok || got.Name != "pod-ready" {
		t.Fatalf("selectPod = (%v, %v), want (pod-ready, true)", got, ok)
	}
}

// TestSelectPod_NoneReady proves an all-unready pod list reports no
// target rather than panicking or returning a bad one.
func TestSelectPod_NoneReady(t *testing.T) {
	notReady := uiTestPod(uiTestPodA)
	notReady.Status.Phase = corev1.PodPending

	if _, ok := selectPod([]corev1.Pod{*notReady}, ""); ok {
		t.Fatal("selectPod: expected no target, got one")
	}
}

// fakeForwarder is uiDeps.Forward's test double: no SPDY, no real
// networking, just enough behavior to drive runUI's reforward loop.
type fakeForwarder struct {
	mu                sync.Mutex
	calls             []string
	secondCallStarted chan struct{}
	firstCallBehavior error // returned immediately after signaling ready
}

func newFakeForwarder(firstCallBehavior error) *fakeForwarder {
	return &fakeForwarder{secondCallStarted: make(chan struct{}), firstCallBehavior: firstCallBehavior}
}

func (f *fakeForwarder) forward(ctx context.Context, pod *corev1.Pod, _, _ int32, ready chan struct{}) error {
	f.mu.Lock()
	f.calls = append(f.calls, pod.Name)
	call := len(f.calls)
	f.mu.Unlock()

	close(ready)

	if call == 1 {
		return f.firstCallBehavior
	}

	close(f.secondCallStarted)
	<-ctx.Done()

	return nil
}

func (f *fakeForwarder) callNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.calls...)
}

// runUITest runs runUI with fwd and waits for its second forward call (the
// re-forward after the first one ends), then cancels ctx and waits for
// runUI to return, failing the test on either timeout.
func runUITest(t *testing.T, k8sClient client.Client, fwd *fakeForwarder) (string, []string) {
	t.Helper()

	var stderr bytes.Buffer

	opened := 0
	deps := uiDeps{
		K8s:         k8sClient,
		Forward:     fwd.forward,
		OpenBrowser: func(string) error { opened++; return nil },
		Stdout:      &bytes.Buffer{},
		Stderr:      &stderr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- runUI(ctx, uiOptions{Namespace: uiTestNamespace, OpenBrowser: true}, deps) }()

	select {
	case <-fwd.secondCallStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the second forward attempt")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runUI did not return after ctx was cancelled")
	}

	if opened != 1 {
		t.Fatalf("OpenBrowser called %d times, want exactly 1", opened)
	}

	return stderr.String(), fwd.callNames()
}

// TestRunUI_ReforwardsToAnotherPodWhenForwardFails proves the behavior
// docs/design/architecture.md names explicitly: when the forwarded pod's
// connection is lost, ui picks a different ready pod and re-establishes
// the tunnel on the same local port, telling the user it did so.
func TestRunUI_ReforwardsToAnotherPodWhenForwardFails(t *testing.T) {
	podA, podB := uiTestPod(uiTestPodA), uiTestPod(uiTestPodB)
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiTestService(), podA, podB).Build()

	fwd := newFakeForwarder(errors.New("simulated connection loss"))

	stderr, calls := runUITest(t, k8sClient, fwd)

	if len(calls) != 2 || calls[0] != uiTestPodA || calls[1] != uiTestPodB {
		t.Fatalf("forward calls = %v, want [pod-a pod-b]", calls)
	}

	if !strings.Contains(stderr, "port-forward to "+uiTestNamespace+"/pod-a") {
		t.Fatalf("missing initial forward message, got: %s", stderr)
	}

	if !strings.Contains(stderr, "lost the port-forward to pod/pod-a") {
		t.Fatalf("missing lost-connection message, got: %s", stderr)
	}

	if !strings.Contains(stderr, "re-established the port-forward -> pod/pod-b") {
		t.Fatalf("missing re-established message, got: %s", stderr)
	}
}

// TestRunUI_ReforwardsWhenTunnelEndsWithoutError proves the other way a
// tunnel can end -- Forward returning nil on its own, not via ctx being
// cancelled -- is treated the same way: reforward, don't exit.
func TestRunUI_ReforwardsWhenTunnelEndsWithoutError(t *testing.T) {
	pod := uiTestPod(uiTestPodA)
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiTestService(), pod).Build()

	fwd := newFakeForwarder(nil)

	stderr, calls := runUITest(t, k8sClient, fwd)

	if len(calls) != 2 || calls[0] != uiTestPodA || calls[1] != uiTestPodA {
		t.Fatalf("forward calls = %v, want [pod-a pod-a]", calls)
	}

	if !strings.Contains(stderr, "the port-forward to pod/pod-a ended; looking for another pod") {
		t.Fatalf("missing ended-without-error message, got: %s", stderr)
	}
}

// TestRunUI_WaitsForAPodToBecomeReady proves the "fake what returns the
// pod list" seam: with zero ready pods at first, runUI prints a waiting
// message and then proceeds once a ready pod actually exists -- no real
// cluster, no real drain, just the fake client gaining a Pod partway
// through.
func TestRunUI_WaitsForAPodToBecomeReady(t *testing.T) {
	oldInterval := uiPollInterval
	uiPollInterval = 5 * time.Millisecond

	t.Cleanup(func() { uiPollInterval = oldInterval })

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiTestService()).Build()

	var stderr bytes.Buffer

	deps := uiDeps{
		K8s: k8sClient,
		Forward: func(ctx context.Context, _ *corev1.Pod, _, _ int32, ready chan struct{}) error {
			close(ready)
			<-ctx.Done()
			return nil
		},
		OpenBrowser: func(string) error { return nil },
		Stdout:      &bytes.Buffer{},
		Stderr:      &stderr,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- runUI(ctx, uiOptions{Namespace: uiTestNamespace}, deps) }()

	time.Sleep(30 * time.Millisecond)

	if err := k8sClient.Create(context.Background(), uiTestPod("pod-late")); err != nil {
		t.Fatalf("creating the late pod: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runUI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runUI did not return after ctx was cancelled")
	}

	if !strings.Contains(stderr.String(), "waiting for a ready ichor-ui pod") {
		t.Fatalf("missing waiting message, got: %s", stderr.String())
	}

	if !strings.Contains(stderr.String(), "pod/pod-late") {
		t.Fatalf("expected the late pod to be forwarded to, got: %s", stderr.String())
	}
}

// TestRunUI_ErrorsWhenServiceMissing proves a missing ichor-ui-service
// fails loudly instead of waiting forever for pods nothing will ever
// list.
func TestRunUI_ErrorsWhenServiceMissing(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	deps := uiDeps{K8s: k8sClient, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	if err := runUI(context.Background(), uiOptions{Namespace: uiTestNamespace}, deps); err == nil {
		t.Fatal("expected an error for a missing Service, got nil")
	}
}
