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
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ADR 0105 migration table, read from fixture Deployments.
func TestReadUISettingsMigrationCases(t *testing.T) {
	live := func(secret string, args ...string) *appsv1.Deployment {
		d := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: UIContainer, Args: args}},
		}}}}
		if secret != "" {
			d.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: UITLSVolume, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secret}}}}
		}

		return d
	}

	for _, tc := range []struct {
		name         string
		dep          *appsv1.Deployment
		listen, http string
	}{
		{"plaintext on 8080 keeps the transitional listener", live("", "--addr=:8080"), "", ":8080"},
		{"TLS on 8080 is pinned", live("cert", "--addr=:8080", "--tls-cert-file=/x"), ":8080", ""},
		{"fresh install serves 8443 only", live("", "--addr=:8443"), "", ""},
		{"fresh install with a Secret serves 8443 only", live("cert", "--addr=:8443"), "", ""},
		{"an install already on N keeps its plaintext listener", live("", "--addr=:8443", "--http-addr=:8080"), "", ":8080"},
		{"no live Deployment", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadUISettings(tc.dep, nil)
			if got.ListenAddr != tc.listen || got.HTTPAddr != tc.http {
				t.Errorf("ListenAddr=%q HTTPAddr=%q, want %q and %q", got.ListenAddr, got.HTTPAddr, tc.listen, tc.http)
			}
		})
	}
}

func TestMigrationShapesTheManifestObjects(t *testing.T) {
	objs, err := Decode([]byte(rendered("ns")))
	must(t, err)

	plain, err := CustomizeObjects(objs, UISettings{HTTPAddr: ":8080"})
	must(t, err)

	c := toDeployment(t, plain[0].Object).Spec.Template.Spec.Containers[0]
	if !slices.Contains(c.Args, "--http-addr=:8080") || !slices.Contains(c.Args, "--addr=:8443") {
		t.Errorf("plaintext case args = %v", c.Args)
	}

	var svc corev1.Service

	must(t, runtime.DefaultUnstructuredConverter.FromUnstructured(plain[2].Object, &svc))

	if len(svc.Spec.Ports) != 2 || svc.Spec.Ports[0].Port != 8443 || svc.Spec.Ports[1].Name != UIPlainPortName || svc.Spec.Ports[1].Port != 8080 {
		t.Errorf("plaintext case Service ports = %+v, want https 8443 and http 8080", svc.Spec.Ports)
	}

	pinned, err := CustomizeObjects(objs, UISettings{ListenAddr: ":8080", TLSSecretName: "cert"})
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
