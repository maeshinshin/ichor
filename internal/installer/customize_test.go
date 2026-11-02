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
	"context"
	"fmt"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testExternalURL = "https://ichor.example.com"
	uiName          = "ichor-ichor-ui"
	ctrlName        = "ichor-controller-manager"
	appKey          = "app"
)

// rendered is the new release's manifest as kustomize emits it: plain tcpSocket probes, no
// customization.
func rendered(ns string) string {
	image := "new:v2"
	dep := func(name, container, extraLabel, args string) string {
		return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
  labels: {app: %[1]s%[5]s}
spec:
  selector:
    matchLabels: {app: %[1]s}
  template:
    metadata:
      labels: {app: %[1]s}
    spec:
      containers:
      - name: %[3]s
        image: %[4]s
        args: [%[6]s]
        ports:
        - {name: https, containerPort: 8443}
        livenessProbe:
          httpGet: {path: /auth/config, port: 8443, scheme: HTTPS}
        readinessProbe:
          httpGet: {path: /auth/config, port: 8443, scheme: HTTPS}
`, name, ns, container, image, extraLabel, args)
	}

	return dep(uiName, UIContainer, ", app.kubernetes.io/component: ui", "--addr=:8443") +
		dep(ctrlName, ControllerContainer, "", "--leader-elect") + fmt.Sprintf(`---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels: {app.kubernetes.io/component: ui}
spec:
  selector: {app: %s}
  ports:
  - {name: https, port: 8443, targetPort: 8443}
`, uiName, ns, uiName)
}

// oldLive is what the previous release's manifest plus `ichor init` left in the cluster, built
// with the same functions init uses.
func oldLive(t *testing.T, c client.Client, ns string, s UISettings) {
	t.Helper()

	meta := func(name string, labels map[string]string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels}
	}
	tcp := &corev1.Probe{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(8080)}}
	podSpec := func(name string, args ...string) appsv1.DeploymentSpec {
		return appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{appKey: name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{appKey: name}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: map[string]string{uiName: UIContainer, ctrlName: ControllerContainer}[name], Image: "old:v1", Args: args,
					LivenessProbe: tcp.DeepCopy(), ReadinessProbe: tcp.DeepCopy(),
				}}},
			},
		}
	}

	ui := &appsv1.Deployment{ObjectMeta: meta(uiName, map[string]string{appKey: uiName}), Spec: podSpec(uiName, "--addr=:8080")}
	ctrl := &appsv1.Deployment{ObjectMeta: meta(ctrlName, map[string]string{appKey: ctrlName}), Spec: podSpec(ctrlName, "--leader-elect")}
	svc := &corev1.Service{
		ObjectMeta: meta(uiName, map[string]string{"app.kubernetes.io/component": "ui"}),
		Spec:       corev1.ServiceSpec{Selector: map[string]string{appKey: uiName}, Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
	}

	if err := CustomizeUI(ui, s); err != nil {
		t.Fatal(err)
	}

	if err := CustomizeController(ctrl, s); err != nil {
		t.Fatal(err)
	}

	CustomizeService(svc, s)

	for _, o := range []client.Object{ui, ctrl, svc} {
		// A manager that is not the installer's, as init's Update is.
		if err := c.Create(context.Background(), o, client.FieldOwner("ichor-init")); err != nil {
			t.Fatalf("creating %T: %v", o, err)
		}
	}
}

func probeHandlers(p *corev1.Probe) (n int) {
	for _, set := range []bool{p.HTTPGet != nil, p.TCPSocket != nil, p.Exec != nil, p.GRPC != nil} {
		if set {
			n++
		}
	}

	return n
}

func TestUpgradeKeepsInitsCustomizationsOverALiveUIDeployment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		settings  UISettings
		wantHTTP  bool
		staleHTTP bool
	}{
		{"stale httpGet probe, no TLS setting", UISettings{ExternalURL: testExternalURL}, true, true},
		{"users on with TLS", UISettings{
			ExternalURL: testExternalURL, TLSSecretName: "ichor-ui-tls",
			KubeAPIServerURL: "https://k8s.example.com:443", KubeAPITLSServerName: "k8s.example.com", RefreshLifetime: "24h",
		}, true, false},
		{"users on without TLS", UISettings{ExternalURL: testExternalURL}, true, false},
		{"users off", UISettings{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()

			oldLive(t, k8sClient, f.ns, tc.settings)

			if tc.staleHTTP {
				// An httpGet probe nothing in the settings accounts for (TLS was removed by hand): the
				// replace must still leave exactly one handler.
				var live appsv1.Deployment

				must(t, k8sClient.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: uiName}, &live))

				lc := &live.Spec.Template.Spec.Containers[0]
				lc.LivenessProbe = &corev1.Probe{HTTPGet: &corev1.HTTPGetAction{Path: "/auth/config", Port: intstr.FromInt32(8080)}}

				must(t, k8sClient.Update(ctx, &live, client.FieldOwner("ichor-init")))
			}

			var order []string

			err := Run(ctx, k8sClient, Options{
				Namespace: f.ns, RunID: f.run, Manifest: []byte(rendered(f.ns)), Ready: noWait(&order),
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			var ui, ctrl appsv1.Deployment

			must(t, k8sClient.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: uiName}, &ui))
			must(t, k8sClient.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: ctrlName}, &ctrl))

			c := ui.Spec.Template.Spec.Containers[0]
			if c.Image != "new:v2" {
				t.Errorf("image = %q, want the new release's", c.Image)
			}

			if tc.settings.TLSSecretName != "" && (c.LivenessProbe.TCPSocket == nil || probeHandlers(c.LivenessProbe) != 1) {
				t.Errorf("liveness must be a lone tcpSocket on a TLS install: %+v", c.LivenessProbe)
			}

			for _, p := range []*corev1.Probe{c.ReadinessProbe} {
				if probeHandlers(p) != 1 || (p.HTTPGet != nil) != tc.wantHTTP {
					t.Errorf("probe has %d handlers, httpGet=%v, want exactly one (httpGet=%v): %+v", probeHandlers(p), p.HTTPGet != nil, tc.wantHTTP, p)
				}
			}

			// Every customization, compared against the pure function over the new manifest.
			// The live objects are the previous release's, on 8080: ADR 0105 pins TLS installs there and
			// moves the others to 8443 (unit 10).
			migrated := tc.settings
			if migrated.TLSSecretName != "" {
				migrated.ListenAddr = plainAddr
			}
			// ADR 0108: an install with no sign-in keeps writing, now by an explicit flag.
			migrated.AllowUnauthenticated = !migrated.UsersOn()

			want := renderedUI(t, f.ns, migrated)
			if got, exp := shape(c, ui.Spec.Template.Spec.Volumes), shape(want.Spec.Template.Spec.Containers[0], want.Spec.Template.Spec.Volumes); got != exp {
				t.Errorf("customizations lost or changed:\n got  %s\n want %s", got, exp)
			}

			if got := slices.Contains(ctrl.Spec.Template.Spec.Containers[0].Args, "--user-issuer"); got != tc.settings.UsersOn() {
				t.Errorf("controller --user-issuer = %v, want %v: %v", got, tc.settings.UsersOn(), ctrl.Spec.Template.Spec.Containers[0].Args)
			}

			if tc.settings.UsersOn() && !slices.Contains(ctrl.Spec.Template.Spec.Containers[0].Args, "--oidc-refresh-lifetime="+orDefault(tc.settings.RefreshLifetime)) {
				t.Errorf("controller refresh lifetime lost: %v", ctrl.Spec.Template.Spec.Containers[0].Args)
			}

			var svc corev1.Service

			must(t, k8sClient.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: uiName}, &svc))

			if got := slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool { return p.Name == UIInternalPortName }); got != tc.settings.UsersOn() {
				t.Errorf("Service internal port = %v, want %v", got, tc.settings.UsersOn())
			}
		})
	}
}

func orDefault(s string) string {
	if s == "" {
		return DefaultRefreshLifetime
	}

	return s
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// renderedUI is the new manifest's ichor-ui Deployment with s applied by the shared function.
func renderedUI(t *testing.T, ns string, s UISettings) *appsv1.Deployment {
	t.Helper()

	objs, err := Decode([]byte(rendered(ns)))
	must(t, err)

	objs, err = CustomizeObjects(objs, s)
	must(t, err)

	for _, o := range objs {
		if o.GetKind() == "Deployment" && o.GetName() == uiName {
			return toDeployment(t, o.Object)
		}
	}

	t.Fatal("no ichor-ui Deployment")

	return nil
}

// Init and the installer must produce identical objects for the same settings: both call
// the same functions, and this pins that the installer's object path (unstructured round
// trip) does not change what a typed call yields.
func TestInstallerAndInitCustomizeIdentically(t *testing.T) {
	s := UISettings{ExternalURL: testExternalURL, TLSSecretName: "my-cert", KubeAPIServerURL: "https://k8s.example.com:443"}

	objs, err := Decode([]byte(rendered("ns")))
	must(t, err)

	got, err := CustomizeObjects(objs, s)
	must(t, err)

	typed := toDeployment(t, objs[0].Object)
	must(t, CustomizeUI(typed, s))

	if !equality.Semantic.DeepEqual(toDeployment(t, got[0].Object).Spec, typed.Spec) {
		t.Errorf("installer path differs from the direct call:\n%+v\n%+v", toDeployment(t, got[0].Object).Spec, typed.Spec)
	}

	// ReadUISettings is the inverse: what init wrote is what the installer reads back.
	ctrl := toDeployment(t, objs[1].Object)
	must(t, CustomizeController(ctrl, s))

	if back := ReadUISettings(typed, ctrl); back.ExternalURL != s.ExternalURL || back.TLSSecretName != s.TLSSecretName ||
		back.KubeAPIServerURL != s.KubeAPIServerURL || back.RefreshLifetime != DefaultRefreshLifetime {
		t.Errorf("settings read back = %+v, wrote %+v", back, s)
	}
}

func TestCustomizeUIReplacesTCPProbesAndIsIdempotent(t *testing.T) {
	objs, err := Decode([]byte(rendered("ns")))
	must(t, err)

	d := toDeployment(t, objs[0].Object)
	s := UISettings{ExternalURL: testExternalURL, TLSSecretName: "c"}

	must(t, CustomizeUI(d, s))

	once := d.DeepCopy()

	must(t, CustomizeUI(d, s))

	if !equality.Semantic.DeepEqual(once.Spec, d.Spec) {
		t.Errorf("customizing twice changed the Deployment")
	}

	c := d.Spec.Template.Spec.Containers[0]
	if p := c.LivenessProbe; p.TCPSocket == nil || p.HTTPGet != nil {
		t.Errorf("liveness = %+v, want tcpSocket only", p)
	}

	if p := c.ReadinessProbe; p.TCPSocket != nil || p.HTTPGet == nil {
		t.Errorf("readiness = %+v, want httpGet only", p)
	}
}

// shape is the customized part of a Deployment, without the defaults the API server adds.
func shape(c corev1.Container, vols []corev1.Volume) string {
	v, m, p := make([]string, 0, len(vols)), make([]string, 0, len(c.VolumeMounts)), make([]string, 0, len(c.Ports))

	for _, x := range vols {
		v = append(v, x.Name+"="+x.Secret.SecretName)
	}

	for _, x := range c.VolumeMounts {
		m = append(m, x.Name+":"+x.MountPath)
	}

	for _, x := range c.Ports {
		p = append(p, fmt.Sprintf("%s:%d", x.Name, x.ContainerPort))
	}

	return fmt.Sprintf("args=%v ports=%v mounts=%v volumes=%v", c.Args, p, m, v)
}

func toDeployment(t *testing.T, obj map[string]any) *appsv1.Deployment {
	t.Helper()

	d := &appsv1.Deployment{}
	must(t, runtime.DefaultUnstructuredConverter.FromUnstructured(obj, d))

	return d
}
