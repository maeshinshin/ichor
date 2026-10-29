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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// newCiliumPreflight is the production ciliumPreflight: it reads the cluster being adopted with
// kube (the DaemonSet and the Helm release records) and c (the live objects of the diff gate).
// Read-only.
func newCiliumPreflight(kube kubernetes.Interface, c client.Reader) ciliumPreflight {
	get := func(ctx context.Context, ref cilium.ObjectRef) (map[string]any, error) {
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return nil, fmt.Errorf("parsing apiVersion %q: %w", ref.APIVersion, err)
		}

		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gv.WithKind(ref.Kind))

		if err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, u); err != nil {
			return nil, err
		}

		return u.Object, nil
	}

	return func(ctx context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
		in.Kube, in.Get = kube, get

		return cilium.Preflight(ctx, in)
	}
}
