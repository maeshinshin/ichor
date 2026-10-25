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

package render

import (
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// None of these fixtures embed real Talos secrets: the bundle is generated
// fresh by machinery's own secrets.NewBundle on every test run, never
// hardcoded, so there is nothing bundle-shaped for this file to leak.

const (
	testNamespace   = "default"
	testClusterName = "test-cluster"
	versionContract = "v1.14"

	layerCluster  = "cluster"
	layerRole     = "role"
	layerSelector = "selector"
	layerMachine  = "machine"

	roleControlPlane = "controlplane"
	roleWorker       = "worker"

	labelEnv = "env"

	sysctlKey = "ichor.test.render"
)

func testContract(t *testing.T) *coreconfig.VersionContract {
	t.Helper()

	contract, err := coreconfig.ParseContractFromVersion(versionContract)
	if err != nil {
		t.Fatalf("ParseContractFromVersion(%q): %v", versionContract, err)
	}

	return contract
}

func testBundle(t *testing.T) *secrets.Bundle {
	t.Helper()

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), testContract(t))
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	return bundle
}

func testCluster(schedulable string) *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{
		Namespace: testNamespace, Name: testClusterName,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.5"},
			TalosVersion:      "v1.14.3",
			KubernetesVersion: constants.DefaultKubernetesVersion,
			VersionContract:   versionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "bundle"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: schedulable},
		},
	}
}

func testMachine(name, role string, labels map[string]string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Namespace: testNamespace, Name: name,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + name},
			Endpoint:   "10.0.0.10:50000",
			Role:       role,
			NodeLabels: labels,
		},
	}
}

// sysctlPatch builds a TalosConfigPatch carrying a single SysctlConfig
// document that sets sysctlKey to value, so tests can assert on exactly
// where a value in the rendered config came from by checking which value
// survived.
func sysctlPatch(
	name, layer string,
	priority int32,
	target *ichorv1alpha1.TalosConfigPatchTarget,
	value string,
) ichorv1alpha1.TalosConfigPatch {
	return ichorv1alpha1.TalosConfigPatch{
		Namespace: testNamespace, Name: name,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
			Layer:      layer,
			Priority:   priority,
			Target:     target,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{
				{
					Kind:         "SysctlConfig",
					SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{sysctlKey: value}},
				},
			},
		},
	}
}

func mustRender(
	t *testing.T,
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	patches []ichorv1alpha1.TalosConfigPatch,
) string {
	t.Helper()

	out, _, err := Render(cluster, m, bundle, patches)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	return string(out)
}

// TestRenderLayerOrdering checks that layers apply bottom-up (cluster, role,
// selector, machine) and that the topmost applicable layer wins.
func TestRenderLayerOrdering(t *testing.T) {
	cluster := testCluster("true")
	labels := map[string]string{labelEnv: "prod"}
	m := testMachine("node-a", roleControlPlane, labels)
	bundle := testBundle(t)

	patches := []ichorv1alpha1.TalosConfigPatch{
		sysctlPatch("p-cluster", layerCluster, 0, nil, "v-cluster"),
		sysctlPatch("p-role", layerRole, 0, &ichorv1alpha1.TalosConfigPatchTarget{Role: roleControlPlane}, "v-role"),
		sysctlPatch("p-selector", layerSelector, 0, &ichorv1alpha1.TalosConfigPatchTarget{
			LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
		}, "v-selector"),
		sysctlPatch("p-machine", layerMachine, 0, &ichorv1alpha1.TalosConfigPatchTarget{
			MachineRef: &corev1.LocalObjectReference{Name: "node-a"},
		}, "v-machine"),
	}

	out := mustRender(t, cluster, m, bundle, patches)

	if !strings.Contains(out, sysctlKey+": v-machine") {
		t.Fatalf("rendered config does not have the machine-layer value (highest layer should win):\n%s", out)
	}

	for _, loser := range []string{"v-cluster", "v-role", "v-selector"} {
		if strings.Contains(out, sysctlKey+": "+loser) {
			t.Fatalf("rendered config still has %q, the machine layer should have overridden it:\n%s", loser, out)
		}
	}
}

// TestRenderPriorityOrdering checks that within one layer, the higher
// priority patch wins.
func TestRenderPriorityOrdering(t *testing.T) {
	cluster := testCluster("true")
	m := testMachine("node-a", roleWorker, nil)
	bundle := testBundle(t)

	patches := []ichorv1alpha1.TalosConfigPatch{
		sysctlPatch("p-low", layerCluster, 1, nil, "v-low"),
		sysctlPatch("p-high", layerCluster, 5, nil, "v-high"),
	}

	out := mustRender(t, cluster, m, bundle, patches)

	if !strings.Contains(out, sysctlKey+": v-high") {
		t.Fatalf("rendered config does not have the higher-priority value:\n%s", out)
	}

	if strings.Contains(out, sysctlKey+": v-low") {
		t.Fatalf("rendered config still has the lower-priority value:\n%s", out)
	}
}

// TestRenderSelection checks step 3 (which patches apply to this machine):
// a selector mismatch, a machineRef for another machine, and a patch
// belonging to another cluster must all be skipped.
func TestRenderSelection(t *testing.T) {
	cluster := testCluster("true")
	m := testMachine("node-a", roleWorker, map[string]string{labelEnv: "prod"})
	bundle := testBundle(t)

	cases := []struct {
		name  string
		patch ichorv1alpha1.TalosConfigPatch
	}{
		{
			name: "selector does not match",
			patch: sysctlPatch("p1", layerSelector, 0, &ichorv1alpha1.TalosConfigPatchTarget{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{labelEnv: "staging"}},
			}, "v-selector"),
		},
		{
			name: "machineRef names a different machine",
			patch: sysctlPatch("p2", layerMachine, 0, &ichorv1alpha1.TalosConfigPatchTarget{
				MachineRef: &corev1.LocalObjectReference{Name: "node-b"},
			}, "v-machine"),
		},
		{
			name: "role does not match",
			patch: sysctlPatch("p3", layerRole, 0, &ichorv1alpha1.TalosConfigPatchTarget{
				Role: roleControlPlane,
			}, "v-role"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{tc.patch})
			if strings.Contains(out, sysctlKey) {
				t.Fatalf("patch should not have applied, but its value is present:\n%s", out)
			}
		})
	}

	t.Run("patch belongs to another cluster", func(t *testing.T) {
		patch := sysctlPatch("p4", layerCluster, 0, nil, "v-other-cluster")
		patch.Spec.ClusterRef.Name = "some-other-cluster"

		out := mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		if strings.Contains(out, sysctlKey) {
			t.Fatalf("patch from another cluster should not have applied:\n%s", out)
		}
	})
}

// TestRenderHashStable checks that the hash is stable across repeated
// renders of the same inputs and across reordering the candidate patch
// list (same effective layering, different slice order), and that it
// changes when a patch does.
func TestRenderHashStable(t *testing.T) {
	cluster := testCluster("true")
	m := testMachine("node-a", roleWorker, nil)
	bundle := testBundle(t)

	roleTarget := &ichorv1alpha1.TalosConfigPatchTarget{Role: roleWorker}
	patches := []ichorv1alpha1.TalosConfigPatch{
		sysctlPatch("p-a", layerCluster, 1, nil, "1"),
		sysctlPatch("p-b", layerRole, 0, roleTarget, "2"),
	}

	out1, hash1, err := Render(cluster, m, bundle, patches)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	out2, hash2, err := Render(cluster, m, bundle, patches)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("hash not stable across repeated renders: %s != %s", hash1, hash2)
	}

	if string(out1) != string(out2) {
		t.Fatalf("rendered bytes not stable across repeated renders")
	}

	reordered := []ichorv1alpha1.TalosConfigPatch{patches[1], patches[0]}

	_, hash3, err := Render(cluster, m, bundle, reordered)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if hash3 != hash1 {
		t.Fatalf("hash changed when candidates were reordered but the layering did not: %s != %s", hash3, hash1)
	}

	changed := []ichorv1alpha1.TalosConfigPatch{
		patches[0],
		sysctlPatch("p-b", layerRole, 0, roleTarget, "different"),
	}

	_, hash4, err := Render(cluster, m, bundle, changed)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if hash4 == hash1 {
		t.Fatalf("hash did not change when a patch's value changed")
	}
}

// TestControlPlaneTaint checks that the control-plane taint is generated
// only when controlPlane.schedulable is "false".
func TestControlPlaneTaint(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("cp-a", roleControlPlane, nil)

	const taint = "node-role.kubernetes.io/control-plane: NoSchedule"

	t.Run("schedulable false gets tainted", func(t *testing.T) {
		out := mustRender(t, testCluster("false"), m, bundle, nil)
		if !strings.Contains(out, taint) {
			t.Fatalf("expected control-plane taint, got:\n%s", out)
		}
	})

	t.Run("schedulable true is not tainted", func(t *testing.T) {
		out := mustRender(t, testCluster("true"), m, bundle, nil)
		if strings.Contains(out, taint) {
			t.Fatalf("expected no control-plane taint, got:\n%s", out)
		}
	})
}

// TestControlPlaneSchedulableRejectsOutOfEnum checks that Render refuses a
// controlPlane.schedulable value outside {true, false} -- "auto" is gone
// (ADR 0008) and the renderer must reject rather than guess which side to
// pick -- with an error that names the field and the accepted values.
func TestControlPlaneSchedulableRejectsOutOfEnum(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("cp-a", roleControlPlane, nil)

	_, _, err := Render(testCluster("auto"), m, bundle, nil)
	if err == nil {
		t.Fatal("expected an error for controlPlane.schedulable \"auto\", got none")
	}

	if !strings.Contains(err.Error(), "controlPlane.schedulable") {
		t.Fatalf("expected error to name the field controlPlane.schedulable, got: %v", err)
	}

	if !strings.Contains(err.Error(), "true") || !strings.Contains(err.Error(), "false") {
		t.Fatalf("expected error to name the accepted values true/false, got: %v", err)
	}
}

// TestRenderBundleLeak forces every render error path this test can reach
// and asserts that none of the bundle's secret material appears in the
// error string. See the function body for the paths this could not force:
// EncodeBytes failing (no reachable trigger through the public API) and the
// container-level cross-document checks that require a discovery/KubeSpan
// combination render does not expose a way to construct (no generate option
// for cluster discovery is wired up in this unit's scope).
func TestRenderBundleLeak(t *testing.T) {
	bundle := testBundle(t)

	secretsMaterial := []string{
		bundle.Secrets.BootstrapToken,
		bundle.TrustdInfo.Token,
		bundle.Cluster.ID,
		bundle.Cluster.Secret,
		string(bundle.Certs.OS.Crt),
		string(bundle.Certs.OS.Key),
		string(bundle.Certs.K8s.Crt),
		string(bundle.Certs.K8s.Key),
	}

	assertNoLeak := func(t *testing.T, err error) {
		t.Helper()

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		for _, s := range secretsMaterial {
			if s == "" {
				continue
			}

			if strings.Contains(err.Error(), s) {
				t.Fatalf("error message contains bundle secret material: %v", err)
			}
		}
	}

	cluster := testCluster("true")
	m := testMachine("node-a", roleWorker, nil)

	t.Run("nil bundle", func(t *testing.T) {
		_, _, err := Render(cluster, m, nil, nil)
		assertNoLeak(t, err)
	})

	t.Run("invalid version contract", func(t *testing.T) {
		bad := testCluster("true")
		bad.Spec.VersionContract = "not-a-version"
		_, _, err := Render(bad, m, bundle, nil)
		assertNoLeak(t, err)
	})

	t.Run("unparseable role", func(t *testing.T) {
		bad := testMachine("node-a", "not-a-role", nil)
		_, _, err := Render(cluster, bad, bundle, nil)
		assertNoLeak(t, err)
	})

	// machine.ParseType("") is TypeWorker, not an error -- a config
	// document's absent machineType has always meant worker. Render must
	// not inherit that for TalosMachineSpec.Role: discovery creates a
	// machine with no role (ADR 0011), and rendering one before approval
	// sets it must fail explicitly rather than silently produce a worker
	// configuration.
	t.Run("empty role", func(t *testing.T) {
		bad := testMachine("node-a", "", nil)
		_, _, err := Render(cluster, bad, bundle, nil)
		assertNoLeak(t, err)
	})

	t.Run("unknown machine type", func(t *testing.T) {
		bad := testMachine("node-a", "unknown", nil)
		_, _, err := Render(cluster, bad, bundle, nil)
		assertNoLeak(t, err)
	})

	t.Run("empty kubernetes version", func(t *testing.T) {
		bad := testCluster("true")
		bad.Spec.KubernetesVersion = ""
		_, _, err := Render(bad, m, bundle, nil)
		assertNoLeak(t, err)
	})

	t.Run("invalid label selector", func(t *testing.T) {
		patch := sysctlPatch("p1", layerSelector, 0, &ichorv1alpha1.TalosConfigPatchTarget{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{Key: labelEnv, Operator: "NotAnOperator"}},
			},
		}, "x")
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		assertNoLeak(t, err)
	})

	t.Run("unknown document kind", func(t *testing.T) {
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "p2",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerCluster,
				Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: "NotARealKind"}},
			},
		}
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		assertNoLeak(t, err)
	})

	t.Run("duplicate v1alpha1.Config document in one patch", func(t *testing.T) {
		doc := ichorv1alpha1.TalosConfigPatchDocument{
			Kind:   "Config",
			Config: &typedconfig.Config{ConfigVersion: "v1alpha1"},
		}
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "p3",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerCluster,
				Documents:  []ichorv1alpha1.TalosConfigPatchDocument{doc, doc},
			},
		}
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		assertNoLeak(t, err)
	})

	t.Run("etcd config on a worker fails validation", func(t *testing.T) {
		doc := ichorv1alpha1.TalosConfigPatchDocument{
			Kind: "Config",
			Config: &typedconfig.Config{
				ConfigVersion: "v1alpha1",
				ClusterConfig: &typedconfig.ClusterConfig{
					EtcdConfig: &typedconfig.EtcdConfig{},
				},
			},
		}
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "p4",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerCluster,
				Documents:  []ichorv1alpha1.TalosConfigPatchDocument{doc},
			},
		}
		// m is a worker; etcd config is only valid on control plane machines.
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		assertNoLeak(t, err)
	})
}
