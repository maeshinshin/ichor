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

package cli

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

// freshFlow is a discovered-cluster adopt against an API server that does not serve the Ichor
// kinds at all (Ichor not installed yet): every read of them is a NoKindMatch error.
func freshFlow(t *testing.T) (*flowEnv, adoptDeps) {
	t.Helper()

	env := newFlowEnv(t, cniCilium, nil)
	deps := env.deps(t)

	deps.K8s = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: ichorv1alpha1.GroupVersion.Group, Kind: "TalosCluster"}}
		},
	}).Build()

	members := []ClusterMember{
		{Hostname: "cp", Addresses: []string{flowCPAddr}, Role: roleControlPlane},
		{Hostname: "wk", Addresses: []string{flowWKAddr}, Role: testRoleWorker},
	}

	deps.CNIUnmanaged = true
	deps.Seed = flowCPAddr
	deps.ContractOverride = testVersionContract
	deps.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		return &membersReader{current: env.live[endpoint], version: testTalosVersion, members: members}, nil
	}
	deps.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "uuid-" + address, Serial: "s"}, nil
	}

	return env, deps
}

func TestAdoptTreatsUnservedIchorKindsAsAFreshCluster(t *testing.T) {
	env, deps := freshFlow(t)
	deps.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	if !strings.Contains(env.stderr.String(), "--plan: nothing was written") {
		t.Fatalf("plan did not finish:\n%s", env.stderr.String())
	}
}

func TestAdoptSavesTheRecoveredBundleOnlyAfterConsent(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*adoptDeps)
		saved int
	}{
		{"plan only", func(d *adoptDeps) { d.PlanOnly = true }, 0},
		{"declined", func(*adoptDeps) {}, 0},
		{"consented", func(d *adoptDeps) { d.AssumeYes = true }, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, deps := freshFlow(t)

			saved := 0
			deps.SaveBundle = func() error { saved++; return nil }
			tc.setup(&deps)

			// The write phase cannot succeed against the stub client; only the order matters here.
			_ = runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)

			if saved != tc.saved {
				t.Fatalf("SaveBundle called %d times, want %d\n%s", saved, tc.saved, env.stderr.String())
			}
		})
	}
}
