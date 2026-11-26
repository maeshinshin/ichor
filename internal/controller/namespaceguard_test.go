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
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

func TestProtectNamespaceAddsTheLabelOnce(t *testing.T) {
	ctx := context.Background()
	patches := 0
	c := fake.NewClientBuilder().
		WithObjects(&corev1.Namespace{Name: trustNamespace, Labels: map[string]string{"keep": "me"}}).
		WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, o ...client.PatchOption) error {
			patches++

			return c.Patch(ctx, obj, p, o...)
		}}).Build()

	for range 2 {
		if err := ProtectNamespace(ctx, c, c, trustNamespace); err != nil {
			t.Fatal(err)
		}
	}

	var ns corev1.Namespace
	if err := c.Get(ctx, client.ObjectKey{Name: trustNamespace}, &ns); err != nil {
		t.Fatal(err)
	}

	if ns.Labels[deleteguard.ProtectedLabel] != deleteguard.ProtectedValue || ns.Labels["keep"] != "me" {
		t.Errorf("labels = %v, want the protected label added and the others kept", ns.Labels)
	}

	if patches != 1 {
		t.Errorf("patches = %d, want 1: the second call must be a no-op", patches)
	}
}

func TestProtectNamespaceUntilDoneLogsOnceAndBacksOff(t *testing.T) {
	tries := 0
	c := fake.NewClientBuilder().
		WithObjects(&corev1.Namespace{Name: trustNamespace}).
		WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, o ...client.GetOption) error {
			tries++
			if tries <= 4 {
				return apierrors.NewForbidden(corev1.Resource("namespaces"), key.Name, errors.New("no rule"))
			}

			return c.Get(ctx, key, obj, o...)
		}}).Build()

	var lines int

	log := funcr.New(func(_, _ string) { lines++ }, funcr.Options{})

	ProtectNamespaceUntilDone(logr.NewContext(context.Background(), log), c, c, trustNamespace, time.Millisecond, 4*time.Millisecond)

	if tries != 5 || lines != 1 {
		t.Errorf("tries = %d, log lines = %d, want 5 tries (four refused) and one line", tries, lines)
	}

	var ns corev1.Namespace
	if err := c.Get(context.Background(), client.ObjectKey{Name: trustNamespace}, &ns); err != nil || ns.Labels[deleteguard.ProtectedLabel] == "" {
		t.Errorf("label after success: %v, %v", ns.Labels, err)
	}
}
