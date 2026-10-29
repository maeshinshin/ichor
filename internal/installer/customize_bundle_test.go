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
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// realBundle decodes the kustomize output the installer embeds (dist/install.yaml and
// dist/network-policies.yaml, written by `make build-installer`, which `make test` runs first), so
// the customisations are checked against the namePrefix-ed names a hand-built object hides.
func realBundle(t *testing.T) (manifest, policies []*unstructured.Unstructured) {
	t.Helper()

	read := func(name string) []*unstructured.Unstructured {
		b, err := os.ReadFile(filepath.Join("..", "..", "dist", name))
		if err != nil {
			t.Skipf("%v; run `make build-installer`", err)
		}

		objs, err := Decode(b)
		must(t, err)

		return objs
	}

	return read("install.yaml"), read("network-policies.yaml")
}

func uiPolicyPorts(t *testing.T, policies []*unstructured.Unstructured) []string {
	t.Helper()

	var out []string

	n := 0

	for _, o := range policies {
		if !isUIPolicy(o) {
			continue
		}

		n++

		ing, _, _ := unstructured.NestedSlice(o.Object, "spec", "ingress")
		for _, tp := range ing[0].(map[string]any)["toPorts"].([]any) {
			for _, p := range tp.(map[string]any)["ports"].([]any) {
				out = append(out, p.(map[string]any)["port"].(string))
			}
		}
	}

	if n != 1 {
		t.Fatalf("the real policies bundle has %d ichor-ui CiliumNetworkPolicies, want 1", n)
	}

	return out
}

func TestCustomizePoliciesOnTheRealBundle(t *testing.T) {
	_, fresh := realBundle(t)
	CustomizePolicies(fresh, UISettings{})

	if got := uiPolicyPorts(t, fresh); len(got) != 1 || got[0] != "8443" {
		t.Errorf("a fresh install opens %v, want only 8443", got)
	}

	for name, s := range map[string]UISettings{
		"pinned TLS port":   {ListenAddr: plainAddr},
		"deprecated plain":  {HTTPAddr: plainAddr},
		"plain beside 8443": {ListenAddr: UIDefaultAddr, HTTPAddr: plainAddr},
	} {
		_, pol := realBundle(t)
		CustomizePolicies(pol, s)

		got := uiPolicyPorts(t, pol)

		found := false

		for _, p := range got {
			found = found || p == "8080"
		}

		if !found || got[0] != "8443" {
			t.Errorf("%s: the ichor-ui policy opens %v, want 8443 and 8080", name, got)
		}
	}
}

// Every customisation must find its object in the real bundle: the Deployments by container, the
// Service by label, the policy by selector. A miss would leave the object stock and unnoticed.
func TestCustomizeObjectsFindsEveryTargetInTheRealBundle(t *testing.T) {
	objs, _ := realBundle(t)

	out, err := CustomizeObjects(objs, UISettings{HTTPAddr: plainAddr})
	must(t, err)

	var uiDeps, ctrlDeps, svcs int

	for _, o := range out {
		switch o.GetKind() {
		case kindDeployment:
			d := &appsv1.Deployment{}
			must(t, runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, d))

			switch {
			case hasContainer(d, UIContainer):
				uiDeps++

				if !hasPort(d.Spec.Template.Spec.Containers[0].Ports, UIPlainPortName, 8080) {
					t.Errorf("Deployment %s has no http port 8080 after customising", o.GetName())
				}
			case hasContainer(d, ControllerContainer):
				ctrlDeps++
			}
		case "Service":
			svc := &corev1.Service{}
			must(t, runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, svc))

			for _, p := range svc.Spec.Ports {
				if p.Name == UIPlainPortName && p.Port == 8080 {
					svcs++
				}
			}
		}
	}

	if uiDeps != 1 || ctrlDeps != 1 || svcs != 1 {
		t.Errorf("real bundle: %d ichor-ui Deployments, %d controller Deployments, %d customised ichor-ui Services; want 1 each", uiDeps, ctrlDeps, svcs)
	}
}

func hasPort(ports []corev1.ContainerPort, name string, n int32) bool {
	for _, p := range ports {
		if p.Name == name && p.ContainerPort == n {
			return true
		}
	}

	return false
}

// The installer manifest's controller Deployment tolerates the control-plane taint, ichor-ui does not
// (the controller drives drains and must be able to reschedule onto a control plane).
func TestRealBundle_ControllerToleratesTheControlPlane(t *testing.T) {
	manifest, _ := realBundle(t)

	for _, o := range manifest {
		if o.GetKind() != "Deployment" {
			continue
		}

		tols, _, _ := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "tolerations")
		has := len(tols) == 1 && tols[0].(map[string]any)["key"] == "node-role.kubernetes.io/control-plane" &&
			tols[0].(map[string]any)["effect"] == "NoSchedule"

		if isController := strings.HasSuffix(o.GetName(), "controller-manager"); has != isController {
			t.Errorf("%s: control-plane toleration = %t, want %t", o.GetName(), has, isController)
		}
	}
}
