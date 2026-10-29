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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// helmRecord is the Secret Helm's storage driver keeps for one revision of a release.
func ichorHelmRecord(t *testing.T, chartName, appVersion string, config map[string]any) *corev1.Secret {
	t.Helper()

	raw, err := json.Marshal(&release.Release{
		Name: ichorChartName, Namespace: ichorControllerNamespace, Version: 1, Config: config,
		Info:  &release.Info{Status: release.StatusDeployed},
		Chart: &chart.Chart{Metadata: &chart.Metadata{Name: chartName, AppVersion: appVersion}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var gz bytes.Buffer

	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(raw)
	_ = zw.Close()

	return &corev1.Secret{
		Name: "sh.helm.release.v1." + chartName + ".v1", Namespace: ichorControllerNamespace,
		Labels: map[string]string{"owner": string(provenanceHelm), "status": "deployed", helmNameLabel: ichorChartName},
		Type:   "helm.sh/release.v1",
		Data:   map[string][]byte{helmReleaseKey: []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))},
	}
}

func controllerDeployment(helm bool) *appsv1.Deployment {
	d := &appsv1.Deployment{
		Name: "ichor-controller-manager", Namespace: ichorControllerNamespace,
		Labels: map[string]string{"control-plane": "controller-manager"},
	}
	if helm {
		d.Labels["app.kubernetes.io/managed-by"] = "Helm"
		d.Labels["app.kubernetes.io/instance"] = ichorChartName
	}

	return d
}

func presenceClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
}

func TestIchorPresent_EachSource(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}

	withManager := func(m string) *ichorv1alpha1.IchorInstallation {
		i := inst.DeepCopy()
		i.Spec.Deployment = &ichorv1alpha1.IchorDeploymentSpec{Manager: m}

		return i
	}

	tests := []struct {
		name                 string
		objs                 []client.Object
		present, helmManaged bool
	}{
		{"nothing", nil, false, false},
		{"IchorInstallation manager helm", []client.Object{withManager("helm")}, true, true},
		{"IchorInstallation manager installer wins over a Helm Deployment", []client.Object{withManager("installer"), controllerDeployment(true)}, true, false},
		{"IchorInstallation without a manager falls back to the Deployment", []client.Object{inst, controllerDeployment(true)}, true, true},
		{"IchorInstallation", []client.Object{inst}, true, false},
		{"Helm release of the chart ichor", []client.Object{ichorHelmRecord(t, "ichor", "0.1.0", nil)}, true, true},
		{"Helm release of another chart", []client.Object{ichorHelmRecord(t, "cilium", "1.0.0", nil)}, false, false},
		{"controller Deployment installed by Helm", []client.Object{controllerDeployment(true)}, true, true},
		{"controller Deployment not installed by Helm", []client.Object{controllerDeployment(false)}, true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ichorPresent(context.Background(), presenceClient(t, tc.objs...))
			if err != nil {
				t.Fatal(err)
			}

			if got.Present() != tc.present || got.HelmManaged() != tc.helmManaged {
				t.Fatalf("present=%t helmManaged=%t, want %t %t", got.Present(), got.HelmManaged(), tc.present, tc.helmManaged)
			}
		})
	}
}

func helmInstallWrite(t *testing.T, gate bool, cni string, values map[string]any) (*bytes.Buffer, client.Client, error) {
	t.Helper()

	c := presenceClient(t, ichorHelmRecord(t, "ichor", "0.1.0", values), controllerDeployment(true))
	in := fullWriteInput(t)
	// A manifest that cannot be applied proves the install step never looks at it.
	in.Ichor = &adoptWriteIchor{
		Manifest: []byte("not a manifest: ["), NetworkPolicies: []byte(testPolicies),
		DetectedCNI: cni, PolicyCRDPresent: gate, CiliumWait: time.Second,
	}
	in.Interval = time.Millisecond

	var stderr bytes.Buffer

	in.Stderr = &stderr
	_, err := writeAdopt(context.Background(), c, in)

	return &stderr, c, err
}

func TestWriteAdopt_HelmInstalledIchorSkipsInstallAndAdvisesPolicies(t *testing.T) {
	stderr, c, err := helmInstallWrite(t, true, string(detectedCilium), nil)
	if err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err == nil {
		t.Fatal("an IchorInstallation was created on a Helm-installed Ichor")
	}

	for _, want := range []string{"networkPolicy.cilium.enabled=true", "helm upgrade ichor"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("advice lacks %q:\n%s", want, stderr)
		}
	}

	pol := &unstructured.Unstructured{}
	pol.SetAPIVersion("cilium.io/v2")
	pol.SetKind("CiliumNetworkPolicy")

	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: writeDenyPolicy}, pol); err == nil {
		t.Error("a network policy was applied into the Helm-owned namespace")
	}
}

// ADR 0104 F2: a skipped install still says why the network policies are not applied.
func TestWriteAdopt_HelmInstalledIchorOnFlannelPrintsThePolicyNotice(t *testing.T) {
	stderr, _, err := helmInstallWrite(t, true, string(detectedFlannel), nil)
	if err != nil {
		t.Fatal(err)
	}

	want := `not applying Ichor's network policies: the detected CNI is "flannel", not cilium`
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("no policy notice, want %q:\n%s", want, stderr)
	}
}

// chartEnabledKey is the key of networkPolicy.cilium.enabled.
const chartEnabledKey = "enabled"

func TestWriteAdopt_HelmInstalledIchorGivesNoAdviceWhenNotNeeded(t *testing.T) {
	enabled := map[string]any{"networkPolicy": map[string]any{string(detectedCilium): map[string]any{chartEnabledKey: true}}}

	for name, tc := range map[string]struct {
		gate   bool
		cni    string
		values map[string]any
	}{
		"the chart already renders them": {true, string(detectedCilium), enabled},
		"the CNI is flannel":             {true, string(detectedFlannel), nil},
		"the policy CRD is missing":      {false, string(detectedCilium), nil},
	} {
		t.Run(name, func(t *testing.T) {
			stderr, _, err := helmInstallWrite(t, tc.gate, tc.cni, tc.values)
			if err != nil {
				t.Fatal(err)
			}

			if strings.Contains(stderr.String(), "networkPolicy.cilium.enabled") {
				t.Fatalf("advice printed:\n%s", stderr)
			}
		})
	}
}

func TestWriteAdopt_ExistingControllerDeploymentSkipsInstall(t *testing.T) {
	c := presenceClient(t, controllerDeployment(false))
	in := fullWriteInput(t)
	in.Ichor = &adoptWriteIchor{Manifest: []byte("not a manifest: [")}
	in.Interval = time.Millisecond

	if _, err := writeAdopt(context.Background(), c, in); err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}
}

func TestCheckIchorPresence(t *testing.T) {
	ctx := context.Background()
	helm := presenceClient(t, ichorHelmRecord(t, "ichor", "0.2.0", nil))

	t.Run("equal versions pass and the plan says Helm owns Ichor", func(t *testing.T) {
		var out bytes.Buffer

		if err := checkIchorPresence(ctx, helm, &out, "v0.2.0", false); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(out.String(), "installed by Helm (release ichor, version 0.2.0); not touching it") {
			t.Fatalf("plan line missing: %q", out.String())
		}
	})

	t.Run("a different version is refused with the helm upgrade command", func(t *testing.T) {
		err := checkIchorPresence(ctx, helm, &bytes.Buffer{}, "v0.3.1", false)
		if err == nil || !strings.Contains(err.Error(), "helm upgrade ichor "+ichorChartRef+" --version 0.3.1 --namespace ichor-system") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a dev CLI cannot be compared", func(t *testing.T) {
		if err := checkIchorPresence(ctx, helm, &bytes.Buffer{}, "dev", false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a version difference on an Ichor-installed Ichor is not this rule's business", func(t *testing.T) {
		inst := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}
		inst.Status.InstalledVersion = "v0.1.0"

		if err := checkIchorPresence(ctx, presenceClient(t, inst), &bytes.Buffer{}, "v0.9.0", false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("--require-ichor refuses a cluster without Ichor", func(t *testing.T) {
		err := checkIchorPresence(ctx, presenceClient(t), &bytes.Buffer{}, "v0.2.0", true)
		if err == nil || !strings.Contains(err.Error(), "install it with Helm first") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("without --require-ichor a cluster without Ichor is fine", func(t *testing.T) {
		if err := checkIchorPresence(ctx, presenceClient(t), &bytes.Buffer{}, "v0.2.0", false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("--require-ichor accepts an existing Ichor", func(t *testing.T) {
		if err := checkIchorPresence(ctx, helm, &bytes.Buffer{}, "v0.2.0", true); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRefuseHelmManaged(t *testing.T) {
	ctx := context.Background()

	err := refuseHelmManaged(ctx, presenceClient(t, ichorHelmRecord(t, "ichor", "0.2.0", nil)))
	if err == nil || !strings.Contains(err.Error(), "helm upgrade ichor") {
		t.Fatalf("Helm-managed: got %v, want a refusal naming helm upgrade", err)
	}

	inst := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}
	if err := refuseHelmManaged(ctx, presenceClient(t, inst)); err != nil {
		t.Fatalf("installer-managed: %v", err)
	}
}

func TestRefuseHelmManaged_ByManagerField(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerHelm}},
	}

	err := refuseHelmManaged(context.Background(), presenceClient(t, inst))
	if err == nil || !strings.Contains(err.Error(), "helm upgrade ichor "+ichorChartRef) {
		t.Fatalf("got %v, want the Helm command", err)
	}
}

// ADR 0105: a seeded IchorInstallation with manager helm, and nothing else yet, is enough for adopt to
// leave the install alone and not apply its manifest.
func TestWriteAdopt_SeededHelmInstallationSkipsInstall(t *testing.T) {
	seeded := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.1.0", Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerHelm}},
	}

	c := presenceClient(t, seeded)
	in := fullWriteInput(t)
	// A manifest that cannot be applied proves the install step never looks at it.
	in.Ichor = &adoptWriteIchor{Manifest: []byte("not a manifest: ["), DetectedCNI: string(detectedFlannel), CiliumWait: time.Second}
	in.Interval = time.Millisecond

	var stderr bytes.Buffer

	in.Stderr = &stderr

	res, err := writeAdopt(context.Background(), c, in)
	if err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}

	if !slices.Contains(res.Unchanged, "Ichor installation") {
		t.Fatalf("unchanged = %v", res.Unchanged)
	}
}
