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
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func refused() error { return &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED} }

// Hardware finding: with one control plane endpoint the Kubernetes API restarts while
// a rotation reaches the hand-off, and the first "connection refused" failed the hand-off, which the operator
// had to repeat by hand. Every API call of the hand-off now waits for the API with backoff, and says so.
func TestRotateCAHandoffWaitsForTheAPIWhileItRestarts(t *testing.T) {
	shrinkFollowTimers(t)

	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)

	secretReads, lists := 0, 0

	flaky := interceptor.NewClient(f.k8s.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				secretReads++
				if secretReads <= 2 {
					return refused()
				}
			}

			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*ichorv1alpha1.TalosOperationList); ok {
				lists++
				if lists <= 2 {
					return refused()
				}
			}

			return c.List(ctx, list, opts...)
		},
	})

	kubeChecks := 0
	checks := handoffChecks{Kube: func(context.Context, string) error {
		kubeChecks++
		if kubeChecks <= 2 {
			return &os.SyscallError{Syscall: "dial tcp 10.0.0.1:6443: connect", Err: syscall.ECONNREFUSED}
		}

		return nil
	}}

	var errw bytes.Buffer

	if _, err := runRotateCAHandoff(context.Background(), handoffDeps{K8s: flaky, Checks: checks, Poll: time.Millisecond},
		f.request(), credOpName, &errw); err != nil {
		t.Fatalf("a restarting API failed the hand-off: %v\n%s", err, errw.String())
	}

	var op ichorv1alpha1.TalosOperation
	if err := f.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); err != nil {
		t.Fatal(err)
	}

	if op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation] != credHandoffToken {
		t.Fatalf("the hand-off was not acknowledged after the retries: %v", op.Annotations)
	}

	if secretReads < 3 || kubeChecks < 3 || lists < 3 {
		t.Fatalf("the calls were not retried: secret reads %d, kube checks %d, lists %d", secretReads, kubeChecks, lists)
	}

	if !strings.Contains(errw.String(), "waiting") || !strings.Contains(errw.String(), "Kubernetes API") {
		t.Fatalf("the wait was not announced: %s", errw.String())
	}
}

func TestRotateCAHandoffGivesUpWhenTheAPIStaysDown(t *testing.T) {
	shrinkFollowTimers(t)

	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)

	checks := handoffChecks{Kube: func(context.Context, string) error { return refused() }}

	_, err := runRotateCAHandoff(context.Background(), handoffDeps{K8s: f.k8s, Checks: checks, Poll: time.Millisecond},
		f.request(), credOpName, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "still unreachable") {
		t.Fatalf("want an error after the retry window, got %v", err)
	}

	var op ichorv1alpha1.TalosOperation
	if gerr := f.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); gerr != nil {
		t.Fatal(gerr)
	}

	if _, set := op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation]; set {
		t.Fatal("acknowledged although the new kubeconfig was never verified")
	}
}
