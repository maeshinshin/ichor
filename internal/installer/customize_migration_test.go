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

package installer

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const plainAddr = ":8080"

// Migration table, read from fixture Deployments.
func TestReadUISettingsMigrationCases(t *testing.T) {
	live := func(secret string, args ...string) *appsv1.Deployment {
		d := uiWith(args)
		if secret != "" {
			d.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: UITLSVolume, Secret: &corev1.SecretVolumeSource{SecretName: secret}}}
		}

		return d
	}

	for _, tc := range []struct {
		name        string
		dep         *appsv1.Deployment
		listen, was string
	}{
		{"plaintext on 8080 moves to HTTPS on 8443", live("", "--addr=:8080"), "", plainAddr},
		{"TLS on 8080 is pinned", live("cert", "--addr=:8080", "--tls-cert-file=/x"), plainAddr, ""},
		{"fresh install serves 8443 only", live("", "--addr=:8443"), "", ""},
		{"fresh install with a Secret serves 8443 only", live("cert", "--addr=:8443"), "", ""},
		{"an install on 8443 with a hand-set --http-addr drops it", live("", "--addr=:8443", "--http-addr=:8080"), "", plainAddr},
		{"no live Deployment", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadUISettings(tc.dep, nil)
			if got.ListenAddr != tc.listen || got.WasPlaintext != tc.was {
				t.Errorf("ListenAddr=%q WasPlaintext=%q, want %q and %q", got.ListenAddr, got.WasPlaintext, tc.listen, tc.was)
			}
		})
	}
}

func TestCustomizePoliciesOpensTheMigratedPort(t *testing.T) {
	plain := []*unstructured.Unstructured{{Object: map[string]any{
		"apiVersion": "cilium.io/v2", "kind": "CiliumNetworkPolicy", "metadata": map[string]any{"name": "prefixed-ui"},
		"spec": map[string]any{"endpointSelector": map[string]any{"matchLabels": map[string]any{"control-plane": "ui-backend"}}, "ingress": []any{map[string]any{"toPorts": []any{map[string]any{
			"ports": []any{map[string]any{"port": "8443", "protocol": "TCP"}},
		}}}}},
	}}}

	CustomizePolicies(plain, UISettings{WasPlaintext: plainAddr})

	ing, _, _ := unstructured.NestedSlice(plain[0].Object, "spec", "ingress")
	if got := ing[0].(map[string]any)["toPorts"].([]any)[0].(map[string]any)["ports"].([]any); len(got) != 1 {
		t.Errorf("a plaintext install opens %v, want 8443 only", got)
	}
}

func TestMigrationShapesTheManifestObjects(t *testing.T) {
	objs, err := Decode([]byte(rendered("ns")))
	must(t, err)

	plain, err := CustomizeObjects(objs, UISettings{WasPlaintext: plainAddr})
	must(t, err)

	c := toDeployment(t, plain[0].Object).Spec.Template.Spec.Containers[0]
	if slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "--http-addr") }) || !slices.Contains(c.Args, "--addr=:8443") {
		t.Errorf("plaintext case args = %v", c.Args)
	}

	var svc corev1.Service

	must(t, runtime.DefaultUnstructuredConverter.FromUnstructured(plain[2].Object, &svc))

	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8443 {
		t.Errorf("plaintext case Service ports = %+v, want https 8443 only", svc.Spec.Ports)
	}

	pinned, err := CustomizeObjects(objs, UISettings{ListenAddr: plainAddr, TLSSecretName: "cert"})
	must(t, err)

	c = toDeployment(t, pinned[0].Object).Spec.Template.Spec.Containers[0]
	if !slices.Contains(c.Args, "--addr=:8080") || slices.Contains(c.Args, "--addr=:8443") || c.Ports[0].ContainerPort != 8080 ||
		c.ReadinessProbe.HTTPGet.Port.IntValue() != 8080 {
		t.Errorf("pinned case = args %v ports %v probe %+v", c.Args, c.Ports, c.ReadinessProbe)
	}

	must(t, runtime.DefaultUnstructuredConverter.FromUnstructured(pinned[2].Object, &svc))

	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 || svc.Spec.Ports[0].TargetPort.IntValue() != 8080 {
		t.Errorf("pinned case Service ports = %+v", svc.Spec.Ports)
	}

	fresh, err := CustomizeObjects(objs, UISettings{ExternalURL: testExternalURL})
	must(t, err)

	ctrl := toDeployment(t, fresh[1].Object).Spec.Template.Spec.Containers[0]
	if !slices.Contains(ctrl.Args, "--ui-external-url="+testExternalURL) {
		t.Errorf("controller args = %v, want --ui-external-url", ctrl.Args)
	}
}
