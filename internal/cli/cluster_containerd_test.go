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
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestSetContainerdDeviceOwnershipWritesTheField(t *testing.T) {
	setAPITestFlags(t, false)

	cl := newTestCluster()
	cl.Spec.VersionContract = kubePrismDocContract
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cl).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	stored := func() *ichorv1alpha1.TalosClusterContainerd {
		var got ichorv1alpha1.TalosCluster
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: opTestCluster}, &got); err != nil {
			t.Fatal(err)
		}

		return got.Spec.Containerd
	}

	if err := runSetDeviceOwnership(context.Background(), c, discoveryCmdIO(""), true); err != nil {
		t.Fatal(err)
	}

	if s := stored(); s == nil || !s.DeviceOwnershipFromSecurityContext {
		t.Fatalf("want true stored, got %+v", s)
	}

	if err := runSetDeviceOwnership(context.Background(), c, discoveryCmdIO(""), false); err != nil {
		t.Fatal(err)
	}

	if s := stored(); s == nil || s.DeviceOwnershipFromSecurityContext {
		t.Fatalf("want false stored, got %+v", s)
	}
}
