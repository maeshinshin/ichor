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
	"time"

	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestImpersonatorAsksOncePerUserWithinTheTTL(t *testing.T) {
	t.Parallel()

	reviews := 0
	allowed := true
	self := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			reviews++
			obj.(*authv1.SelfSubjectAccessReview).Status.Allowed = allowed

			return nil
		},
	}).Build()

	clock := time.Unix(1000, 0)
	imp := impersonatorVia(&rest.Config{Host: "http://127.0.0.1:1"}, client.Options{Scheme: scheme.Scheme}, 0, self, nil, func() time.Time { return clock })
	id := Identity{User: "ichor:a", Source: SourceIchor}

	for range 2 {
		if _, err := imp(id); err != nil {
			t.Fatal(err)
		}
	}

	if reviews != 1 {
		t.Fatalf("two calls inside the TTL made %d reviews, want 1", reviews)
	}

	clock = clock.Add(impersonationRuleTTL)

	if _, err := imp(id); err != nil || reviews != 2 {
		t.Fatalf("after the TTL: err=%v reviews=%d, want a fresh review", err, reviews)
	}

	// A refusal is never remembered: the next call asks again.
	allowed = false
	other := Identity{User: "ichor:b", Source: SourceIchor}

	for range 2 {
		if _, err := imp(other); err == nil {
			t.Fatal("want a refusal while the rule is missing")
		}
	}

	if reviews != 4 {
		t.Fatalf("reviews = %d, want 4 (a refusal is not cached)", reviews)
	}
}
