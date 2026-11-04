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
	"sync"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
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

	kindConfig            = "Config"
	configVersionV1Alpha1 = "v1alpha1"

	// handoverCompleteReason mirrors internal/controller's
	// reasonHandoverComplete -- duplicated here, not imported, since this
	// package cannot depend on internal/controller.
	handoverCompleteReason  = "Complete"
	handoverCompleteMessage = "handed over"
)

func testContract(t *testing.T) *coreconfig.VersionContract {
	t.Helper()

	contract, err := coreconfig.ParseContractFromVersion(versionContract)
	if err != nil {
		t.Fatalf("ParseContractFromVersion(%q): %v", versionContract, err)
	}

	return contract
}

var (
	sharedBundleOnce sync.Once
	sharedBundle     *secrets.Bundle
)

// testBundle returns one shared, read-only bundle: generating one costs a 4096-bit RSA key.
func testBundle(t *testing.T) *secrets.Bundle {
	t.Helper()

	sharedBundleOnce.Do(func() {
		bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), testContract(t))
		if err != nil {
			t.Fatalf("NewBundle: %v", err)
		}

		sharedBundle = bundle
	})

	return sharedBundle
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

	out, _, err := Render(cluster, m, bundle, patches, nil)
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

	requireAll(t, out, sysctlKey+": v-machine")
	requireNone(t, out, sysctlKey+": v-cluster", sysctlKey+": v-role", sysctlKey+": v-selector")
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

	requireAll(t, out, sysctlKey+": v-high")
	requireNone(t, out, sysctlKey+": v-low")
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
			requireNone(t, mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{tc.patch}), sysctlKey)
		})
	}

	t.Run("patch belongs to another cluster", func(t *testing.T) {
		patch := sysctlPatch("p4", layerCluster, 0, nil, "v-other-cluster")
		patch.Spec.ClusterRef.Name = "some-other-cluster"

		requireNone(t, mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}), sysctlKey)
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

	out1, hash1, err := Render(cluster, m, bundle, patches, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	out2, hash2, err := Render(cluster, m, bundle, patches, nil)
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

	_, hash3, err := Render(cluster, m, bundle, reordered, nil)
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

	_, hash4, err := Render(cluster, m, bundle, changed, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if hash4 == hash1 {
		t.Fatalf("hash did not change when a patch's value changed")
	}
}

// TestControlPlaneTaint checks that the control-plane taint is generated
// only when controlPlane.schedulable is "false".
// TestKubernetesVersionRendersValidImages guards against a doubled "v": spec.kubernetesVersion
// carries the "v" (CRD pattern) and the generator adds its own to the component images.
func TestKubernetesVersionRendersValidImages(t *testing.T) {
	cluster := testCluster("false")
	cluster.Spec.KubernetesVersion = "v1.36.1"

	out := mustRender(t, cluster, testMachine("cp-a", roleControlPlane, nil), testBundle(t), nil)

	requireNone(t, out, ":vv")
	requireAll(t, out, "kube-apiserver:v1.36.1")
}

func TestControlPlaneTaint(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("cp-a", roleControlPlane, nil)

	const taint = "node-role.kubernetes.io/control-plane: NoSchedule"

	t.Run("schedulable false gets tainted", func(t *testing.T) {
		requireAll(t, mustRender(t, testCluster("false"), m, bundle, nil), taint)
	})

	t.Run("schedulable true is not tainted", func(t *testing.T) {
		requireNone(t, mustRender(t, testCluster("true"), m, bundle, nil), taint)
	})
}

// TestControlPlaneSchedulableRejectsOutOfEnum checks that Render refuses a
// controlPlane.schedulable value outside {true, false} -- "auto" is gone
// (ADR 0008) and the renderer must reject rather than guess which side to
// pick -- with an error that names the field and the accepted values.
func TestControlPlaneSchedulableRejectsOutOfEnum(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("cp-a", roleControlPlane, nil)

	_, _, err := Render(testCluster("auto"), m, bundle, nil, nil)
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
		_, _, err := Render(cluster, m, nil, nil, nil)
		assertNoLeak(t, err)
	})

	t.Run("invalid version contract", func(t *testing.T) {
		bad := testCluster("true")
		bad.Spec.VersionContract = "not-a-version"
		_, _, err := Render(bad, m, bundle, nil, nil)
		assertNoLeak(t, err)
	})

	t.Run("unparseable role", func(t *testing.T) {
		bad := testMachine("node-a", "not-a-role", nil)
		_, _, err := Render(cluster, bad, bundle, nil, nil)
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
		_, _, err := Render(cluster, bad, bundle, nil, nil)
		assertNoLeak(t, err)
	})

	t.Run("unknown machine type", func(t *testing.T) {
		bad := testMachine("node-a", "unknown", nil)
		_, _, err := Render(cluster, bad, bundle, nil, nil)
		assertNoLeak(t, err)
	})

	t.Run("empty kubernetes version", func(t *testing.T) {
		bad := testCluster("true")
		bad.Spec.KubernetesVersion = ""
		_, _, err := Render(bad, m, bundle, nil, nil)
		assertNoLeak(t, err)
	})

	t.Run("invalid label selector", func(t *testing.T) {
		patch := sysctlPatch("p1", layerSelector, 0, &ichorv1alpha1.TalosConfigPatchTarget{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{{Key: labelEnv, Operator: "NotAnOperator"}},
			},
		}, "x")
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
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
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
		assertNoLeak(t, err)
	})

	t.Run("duplicate v1alpha1.Config document in one patch", func(t *testing.T) {
		doc := ichorv1alpha1.TalosConfigPatchDocument{
			Kind:   kindConfig,
			Config: &typedconfig.Config{ConfigVersion: configVersionV1Alpha1},
		}
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "p3",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerCluster,
				Documents:  []ichorv1alpha1.TalosConfigPatchDocument{doc, doc},
			},
		}
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
		assertNoLeak(t, err)
	})

	t.Run("etcd config on a worker fails validation", func(t *testing.T) {
		doc := ichorv1alpha1.TalosConfigPatchDocument{
			Kind: kindConfig,
			Config: &typedconfig.Config{
				ConfigVersion: configVersionV1Alpha1,
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
		_, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
		assertNoLeak(t, err)
	})
}

// inlineManifestPatch builds a TalosConfigPatch carrying a single Config
// document whose ClusterConfig.ClusterInlineManifests holds one entry named
// name, alongside an unrelated EtcdConfig document -- so a test can check
// that dropping the inline manifest entry never disturbs a sibling document
// in the same patch.
func inlineManifestPatch(patchName, manifestName string) ichorv1alpha1.TalosConfigPatch {
	return ichorv1alpha1.TalosConfigPatch{
		Namespace: testNamespace, Name: patchName,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
			Layer:      layerCluster,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{
				{
					Kind: kindConfig,
					Config: &typedconfig.Config{
						ConfigVersion: configVersionV1Alpha1,
						ClusterConfig: &typedconfig.ClusterConfig{
							ClusterInlineManifests: []*typedconfig.ClusterInlineManifest{
								{InlineManifestName: manifestName, InlineManifestContents: "kind: Nothing\n"},
							},
							ExtraManifests: []string{"https://example.invalid/unrelated.yaml"},
						},
					},
				},
			},
		},
	}
}

// TestCNIHandoverDropsInlineManifest checks that Render stops emitting the
// CNI's cluster.inlineManifests entry once (and only once)
// render.ConditionCNIHandover is True on the cluster -- the mechanism ADR
// 0023 relies on to make hand-over actually stick: Talos's own
// ManifestApplyController re-applies cluster.inlineManifests on every
// upgrade and has no delete path, so the entry must stop being rendered, not
// merely be adopted by Argo CD.
func TestCNIHandoverDropsInlineManifest(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("node-a", roleWorker, nil)
	patch := inlineManifestPatch("cni-manifests", cni.InlineManifestName)

	const (
		cniManifest       = "name: " + cni.InlineManifestName
		unrelatedManifest = "unrelated.yaml"
	)

	t.Run("condition absent keeps the entry", func(t *testing.T) {
		requireAll(t, mustRender(t, testCluster("true"), m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}), cniManifest)
	})

	t.Run("condition false keeps the entry", func(t *testing.T) {
		cluster := testCluster("true")
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ConditionCNIHandover, Status: metav1.ConditionFalse, Reason: "WaitingForSync", Message: "not yet",
		})

		requireAll(t, mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}), cniManifest)
	})

	t.Run("condition true drops only the CNI entry", func(t *testing.T) {
		cluster := testCluster("true")
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ConditionCNIHandover, Status: metav1.ConditionTrue,
			Reason: handoverCompleteReason, Message: handoverCompleteMessage,
		})

		out := mustRender(t, cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
		requireNone(t, out, cniManifest)
		requireAll(t, out, unrelatedManifest)
	})

	// The original patch object handed to Render must never be mutated: a
	// caller (TalosClusterReconciler, re-rendering every machine in a loop)
	// reuses the same candidate slice across calls, and the condition can be
	// true for one reconcile and absent in a cached copy for another.
	t.Run("does not mutate the caller's patch", func(t *testing.T) {
		cluster := testCluster("true")
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ConditionCNIHandover, Status: metav1.ConditionTrue,
			Reason: handoverCompleteReason, Message: handoverCompleteMessage,
		})

		before := patch.DeepCopy()

		if _, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil); err != nil {
			t.Fatalf("Render: %v", err)
		}

		manifests := patch.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests
		if len(manifests) != len(before.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests) {
			t.Fatalf("Render mutated the caller's TalosConfigPatch: got %d inline manifests, want %d",
				len(manifests), len(before.Spec.Documents[0].Config.ClusterConfig.ClusterInlineManifests))
		}
	})

	t.Run("a patch without the entry is passed through unchanged (not deep-copied)", func(t *testing.T) {
		cluster := testCluster("true")
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: ConditionCNIHandover, Status: metav1.ConditionTrue,
			Reason: handoverCompleteReason, Message: handoverCompleteMessage,
		})

		plain := sysctlPatch("plain", layerCluster, 0, nil, "1")
		ordered, err := SelectPatches(cluster, m, []ichorv1alpha1.TalosConfigPatch{plain})
		if err != nil {
			t.Fatalf("SelectPatches: %v", err)
		}

		dropped := dropHandedOverInlineManifest(ordered, "")
		if len(dropped) != 1 || dropped[0] != ordered[0] {
			t.Fatalf("expected the untouched patch to pass through as the same pointer, got a copy")
		}
	})
}
