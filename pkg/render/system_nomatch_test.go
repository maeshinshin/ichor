/*
Copyright 2026.

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

package render

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Before Ichor is installed (a first adopt) the API server does not serve TalosStorage: that is
// no storage, not an error.
func TestResolveSystemInputsUnservedStorageKind(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "ichor.maesh.dev", Kind: "TalosStorage"}}
		},
	}).Build()

	got, err := ResolveSystemInputs(context.Background(), c, registrySecretNamespace, registryCluster())
	if err != nil {
		t.Fatalf("ResolveSystemInputs: %v", err)
	}

	if len(got.Storage) != 0 {
		t.Fatalf("storage = %v, want none", got.Storage)
	}
}
