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

// package render_test, not render: TestRegistryAuthDriftPathsNeverLeakValue needs
// internal/controller.ChangedPaths, which itself imports pkg/render -- an external test package
// is what lets this file import both without an import cycle (internal/controller -> pkg/render
// is fine; nothing imports this package back). It drives render only through its public API, the
// same surface internal/controller's own reconciler uses.
package render_test

import (
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/render"
)

// TestRegistryAuthDriftPathsNeverLeakValue checks the guarantee internal/controller's
// ChangedPaths (diff.go) relies on for every other field and
// must hold here too: a credential change surfaces only as a field-path string
// ("machine.registries.config.<host>.auth.password"), never as the value itself -- which is what
// keeps drift detection from putting a registry password into TalosMachine.status.
func TestRegistryAuthDriftPathsNeverLeakValue(t *testing.T) {
	const (
		oldSecret = "old-canary-pw-should-never-appear"
		newSecret = "new-canary-pw-should-never-appear"
		host      = "registry.example.com"
	)

	cluster := &ichorv1alpha1.TalosCluster{
		Namespace: "default", Name: "test-cluster",
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.5"},
			TalosVersion:      "v1.14.3",
			KubernetesVersion: constants.DefaultKubernetesVersion,
			VersionContract:   "v1.14",
			SecretsRef:        corev1.LocalObjectReference{Name: "bundle"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: "true"},
			Registries: []ichorv1alpha1.TalosClusterRegistry{
				{Host: host, Endpoints: []string{"https://mirror.example.com"}, Username: "deploy"},
			},
		},
	}
	m := &ichorv1alpha1.TalosMachine{
		Namespace: "default", Name: "node-a",
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-node-a"},
			Endpoint:   "10.0.0.10:50000",
			Role:       "worker",
		},
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		t.Fatalf("ParseContractFromVersion: %v", err)
	}

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	oldAuth := map[string]render.RegistryAuth{host: {Password: oldSecret}}
	newAuth := map[string]render.RegistryAuth{host: {Password: newSecret}}

	oldCfg, _, err := render.Render(cluster, m, bundle, nil, &render.SystemInputs{RegistryAuth: oldAuth})
	if err != nil {
		t.Fatalf("Render (old): %v", err)
	}

	newCfg, _, err := render.Render(cluster, m, bundle, nil, &render.SystemInputs{RegistryAuth: newAuth})
	if err != nil {
		t.Fatalf("Render (new): %v", err)
	}

	paths, err := controller.ChangedPaths(oldCfg, newCfg)
	if err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}

	found := false

	for _, p := range paths {
		if strings.Contains(p, oldSecret) || strings.Contains(p, newSecret) {
			t.Fatalf("a changed-field path leaked the credential value: %q", p)
		}

		if strings.Contains(p, "registries") && strings.Contains(p, "password") {
			found = true
		}
	}

	if !found {
		t.Fatalf("expected a path naming the registry password field, got: %v", paths)
	}
}
