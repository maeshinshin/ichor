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
	"io"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/pkg/cni/gatewayapi"
)

// gatewayCRDRecorder is a client whose only call is Apply; any other method is nil and panics, so a test
// fails if the CRD path ever reads, patches or deletes anything.
type gatewayCRDRecorder struct {
	client.Client

	names  []runtime.ApplyConfiguration
	owners []string
}

func (r *gatewayCRDRecorder) Apply(_ context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	o := &client.ApplyOptions{}
	for _, opt := range opts {
		opt.ApplyToApply(o)
	}

	r.owners = append(r.owners, o.FieldManager)

	r.names = append(r.names, obj)

	return nil
}

// TestApplyGatewayAPICRDsOnlyApplies applies the seven CRDs under their own field manager and never
// deletes, reads or patches anything (the recorder's other methods panic).
func TestApplyGatewayAPICRDsOnlyApplies(t *testing.T) {
	r := &gatewayCRDRecorder{}

	if err := applyGatewayAPICRDs(context.Background(), r, io.Discard); err != nil {
		t.Fatalf("applyGatewayAPICRDs: %v", err)
	}

	want := []string{"backendtlspolicies", "gatewayclasses", "gateways", "grpcroutes", "httproutes", "referencegrants", "tlsroutes"}

	if len(r.names) != len(want) {
		t.Fatalf("applied %d CRDs, want %d", len(r.names), len(want))
	}

	objs, err := installer.Decode(gatewayapi.CRDs())
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, o := range objs {
		names = append(names, o.GetName())
	}

	for _, w := range want {
		if !slices.Contains(names, w+".gateway.networking.k8s.io") {
			t.Errorf("the embedded CRDs lack %s.gateway.networking.k8s.io", w)
		}
	}

	for _, o := range r.owners {
		if o != gatewayAPIFieldOwner {
			t.Fatalf("applied under field manager %q, want %q", o, gatewayAPIFieldOwner)
		}
	}
}
