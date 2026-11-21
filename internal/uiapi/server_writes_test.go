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

package uiapi

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type applyConfig struct{}

func (applyConfig) IsApplyConfiguration() {}

// Every writing method of the Server reaches the Kubernetes API as the caller when an Identity is on the context, and
// as the ServiceAccount only when there is none.
func TestEveryServerWriteMethodRunsAsTheCaller(t *testing.T) {
	var asUserCalls, asSACalls []string

	record := func(into *[]string, name string) { *into = append(*into, name) }

	funcs := func(into *[]string) interceptor.Funcs {
		return interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
				record(into, "Patch")

				return c.Patch(ctx, o, p, opts...)
			},
			DeleteAllOf: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteAllOfOption) error {
				record(into, "DeleteAllOf")

				return c.DeleteAllOf(ctx, o, opts...)
			},
			Apply: func(_ context.Context, _ client.WithWatch, _ runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
				record(into, "Apply")

				return nil
			},
			SubResourceApply: func(_ context.Context, _ client.Client, sub string, _ runtime.ApplyConfiguration, _ ...client.SubResourceApplyOption) error {
				record(into, "SubResourceApply:"+sub)

				return nil
			},
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, o client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
				record(into, "SubResourcePatch:"+sub)

				return nil
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, o client.Object, opts ...client.SubResourceUpdateOption) error {
				record(into, "SubResourceUpdate:"+sub)

				return nil
			},
			SubResourceCreate: func(ctx context.Context, c client.Client, sub string, o client.Object, sc client.Object, opts ...client.SubResourceCreateOption) error {
				record(into, "SubResourceCreate:"+sub)

				return nil
			},
		}
	}

	cm := func() *corev1.ConfigMap {
		c := &corev1.ConfigMap{}
		c.Name, c.Namespace = "x", "n"

		return c
	}
	sa := interceptor.NewClient(fake.NewClientBuilder().WithObjects(cm()).Build(), funcs(&asSACalls))
	user := interceptor.NewClient(fake.NewClientBuilder().WithObjects(cm()).Build(), funcs(&asUserCalls))
	s := &Server{Client: sa, Impersonate: func(Identity) (client.Client, error) { return user, nil }}
	ctx := asUser(context.Background())

	raw := client.RawPatch("application/merge-patch+json", []byte(`{}`))
	steps := []func() error{
		func() error { return s.Patch(ctx, cm(), raw) },
		func() error { return s.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace("n")) },
		func() error { return s.Apply(ctx, applyConfig{}) },
		func() error { return s.Status().Apply(ctx, applyConfig{}) },
		func() error { return s.Status().Update(ctx, cm()) },
		func() error { return s.Status().Patch(ctx, cm(), raw) },
		func() error { return s.SubResource("scale").Create(ctx, cm(), cm()) },
	}

	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	if len(asSACalls) != 0 {
		t.Fatalf("writes ran as the ServiceAccount: %v", asSACalls)
	}

	if len(asUserCalls) != len(steps) {
		t.Fatalf("want %d writes as the caller, got %v", len(steps), asUserCalls)
	}
}
