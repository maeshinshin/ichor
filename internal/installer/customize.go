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
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/oidctrust"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// The customizations `ichor init --ui-url` makes to the manifest it installs. The manifest knows nothing of them, so
// `ichor init` and every upgrade run the
// same pure functions over the rendered objects: init over the live ones, the installer over
// the manifest's, with settings it reads back from the live ichor-ui and controller
// Deployments (ReadUISettings). Nothing is merged with what the cluster holds.
const (
	// UIContainer and ControllerContainer are the container names on every install path.
	UIContainer         = "ichor-ui"
	ControllerContainer = "manager"

	// DefaultRefreshLifetime is the chart's ichorUI.users.refreshLifetime default.
	DefaultRefreshLifetime = "12h"

	UIInternalPort     = 8444
	UIInternalPortName = "internal"

	// UITLSVolume and UITLSMountPath are where the serving certificate Secret is mounted.
	UITLSVolume    = "tls"
	UITLSMountPath = "/etc/ichor/tls"

	argExternalURL     = "--external-url="
	argKubeAPIURL      = "--kube-api-server-url="
	argKubeAPIName     = "--kube-api-tls-server-name="
	argRefreshLifetime = "--oidc-refresh-lifetime="
)

// UISettings is everything init customizes. The zero value is a plain install (users off).
type UISettings struct {
	// ExternalURL turns users on: the https URL ichor-ui is reached at.
	ExternalURL string

	// KubeAPIServerURL and KubeAPITLSServerName are the optional kubeconfig flags.
	KubeAPIServerURL     string
	KubeAPITLSServerName string

	// TLSSecretName, when set, makes ichor-ui serve HTTPS from this kubernetes.io/tls Secret.
	TLSSecretName string

	// RefreshLifetime is the controller's --oidc-refresh-lifetime; empty means the default.
	RefreshLifetime string
}

// UsersOn reports whether users are enabled.
func (s UISettings) UsersOn() bool { return s.ExternalURL != "" }

func container(dep *appsv1.Deployment, name string) (*corev1.Container, error) {
	i := slices.IndexFunc(dep.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("the Deployment %s has no container %q", dep.Name, name)
	}

	return &dep.Spec.Template.Spec.Containers[i], nil
}

func argValue(args []string, prefix string) string {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, prefix); ok {
			return v
		}
	}

	return ""
}

// ReadUISettings recovers the settings from the live Deployments: the flags, and the TLS
// volume, are the record of what init was asked for. A nil or odd Deployment reads as default.
func ReadUISettings(ui, controller *appsv1.Deployment) UISettings {
	var s UISettings

	if ui != nil {
		if c, err := container(ui, UIContainer); err == nil {
			s.ExternalURL = argValue(c.Args, argExternalURL)
			s.KubeAPIServerURL = argValue(c.Args, argKubeAPIURL)
			s.KubeAPITLSServerName = argValue(c.Args, argKubeAPIName)
		}

		for _, v := range ui.Spec.Template.Spec.Volumes {
			if v.Name == UITLSVolume && v.Secret != nil {
				s.TLSSecretName = v.Secret.SecretName
			}
		}
	}

	if controller != nil {
		if c, err := container(controller, ControllerContainer); err == nil {
			s.RefreshLifetime = argValue(c.Args, argRefreshLifetime)
		}
	}

	return s
}

// CustomizeUI applies s to an ichor-ui Deployment: users, TLS and kubeconfig flags. It is
// idempotent, and it replaces probes it owns, so a tcpSocket probe from the manifest never
// ends up beside an httpGet one.
func CustomizeUI(dep *appsv1.Deployment, s UISettings) error {
	c, err := container(dep, UIContainer)
	if err != nil {
		return err
	}

	// Drop any earlier setting of the same flags so a re-run replaces them.
	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return a == "--users-enabled=true" || strings.HasPrefix(a, argExternalURL) || strings.HasPrefix(a, "--internal-jwks-uri=") ||
			strings.HasPrefix(a, argKubeAPIURL) || strings.HasPrefix(a, argKubeAPIName)
	})

	if s.UsersOn() {
		// --internal-jwks-uri is dropped on purpose: ichor-ui answers with the host the apiserver
		// used, so a recreated Service (new ClusterIP) needs no change here.
		c.Args = append(c.Args, "--users-enabled=true", argExternalURL+s.ExternalURL)
		usersVolumes(dep, c)
	}

	if s.KubeAPIServerURL != "" {
		c.Args = append(c.Args, argKubeAPIURL+s.KubeAPIServerURL)
	}

	if s.KubeAPITLSServerName != "" {
		c.Args = append(c.Args, argKubeAPIName+s.KubeAPITLSServerName)
	}

	if s.TLSSecretName == "" {
		return nil
	}

	return tlsOnUI(dep, c, s.TLSSecretName)
}

func usersVolumes(dep *appsv1.Deployment, c *corev1.Container) {
	if !slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool { return p.Name == UIInternalPortName }) {
		c.Ports = append(c.Ports, corev1.ContainerPort{Name: UIInternalPortName, ContainerPort: UIInternalPort, Protocol: corev1.ProtocolTCP})
	}

	// Written by the controller; optional so ichor-ui starts before it has made them.
	optional := true

	for _, v := range []struct{ name, secret, path string }{
		{"oidc-signing", ichorauth.SigningSecretName, "/etc/ichor/oidc-signing"},
		{"oidc-tls", "ichor-oidc-tls", "/etc/ichor/oidc-tls"},
	} {
		if !slices.ContainsFunc(dep.Spec.Template.Spec.Volumes, func(x corev1.Volume) bool { return x.Name == v.name }) {
			dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes, corev1.Volume{
				Name:   v.name,
				Secret: &corev1.SecretVolumeSource{SecretName: v.secret, Optional: &optional},
			})
		}

		if !slices.ContainsFunc(c.VolumeMounts, func(x corev1.VolumeMount) bool { return x.Name == v.name }) {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: v.name, MountPath: v.path, ReadOnly: true})
		}
	}
}

func tlsOnUI(dep *appsv1.Deployment, c *corev1.Container, secret string) error {
	port, err := uiListenPort(c.Args)
	if err != nil {
		return fmt.Errorf("ichor-ui Deployment %s: %w", dep.Name, err)
	}

	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return strings.HasPrefix(a, "--tls-cert-file=") || strings.HasPrefix(a, "--tls-key-file=")
	})
	c.Args = append(c.Args, "--tls-cert-file="+UITLSMountPath+"/tls.crt", "--tls-key-file="+UITLSMountPath+"/tls.key")

	// A kubelet TCP probe makes a TLS server log a handshake error every few seconds, so probe over HTTPS.
	// Whole probes are replaced: the handler is a oneof, and a tcpSocket left beside an httpGet is invalid.
	probe := func(period int32) *corev1.Probe {
		return &corev1.Probe{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/auth/config", Port: intstr.FromInt32(port), Scheme: corev1.URISchemeHTTPS,
			},
			InitialDelaySeconds: 5, PeriodSeconds: period,
		}
	}
	c.LivenessProbe, c.ReadinessProbe = probe(20), probe(10)

	vols := slices.DeleteFunc(dep.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == UITLSVolume })
	dep.Spec.Template.Spec.Volumes = append(vols, corev1.Volume{
		Name: UITLSVolume, Secret: &corev1.SecretVolumeSource{SecretName: secret},
	})

	c.VolumeMounts = slices.DeleteFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == UITLSVolume })
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: UITLSVolume, MountPath: UITLSMountPath, ReadOnly: true})

	return nil
}

// uiListenPort reads the port of ichor-ui's --addr.
func uiListenPort(args []string) (int32, error) {
	for _, a := range args {
		v, ok := strings.CutPrefix(a, "--addr=")
		if !ok {
			continue
		}

		_, p, err := net.SplitHostPort(v)
		if err != nil {
			return 0, fmt.Errorf("cannot read the port from %q: %w", a, err)
		}

		n, err := strconv.ParseInt(p, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("cannot read the port from %q: %w", a, err)
		}

		return int32(n), nil
	}

	return 0, errors.New("no --addr argument to take the listening port from")
}

// CustomizeController adds the flags that make the controller keep the signing keys, internal
// TLS and trust ConfigMap (the chart renders the same two). With users off it removes them.
func CustomizeController(dep *appsv1.Deployment, s UISettings) error {
	c, err := container(dep, ControllerContainer)
	if err != nil {
		return err
	}

	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return a == "--user-issuer" || a == "--user-issuer=true" || strings.HasPrefix(a, argRefreshLifetime)
	})

	if !s.UsersOn() {
		return nil
	}

	life := s.RefreshLifetime
	if life == "" {
		life = DefaultRefreshLifetime
	}

	c.Args = append(c.Args, "--user-issuer", argRefreshLifetime+life)

	return nil
}

// CustomizeService adds the internal port users need to the ichor-ui Service.
func CustomizeService(svc *corev1.Service, s UISettings) {
	if !s.UsersOn() || slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool { return p.Name == UIInternalPortName }) {
		return
	}

	svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
		Name: UIInternalPortName, Port: UIInternalPort, TargetPort: intstr.FromInt32(UIInternalPort), Protocol: corev1.ProtocolTCP,
	})
}

// IsUIService reports whether svc is the ichor-ui Service.
func IsUIService(svc *corev1.Service) bool {
	return svc.Labels[oidctrust.UIComponentLabel] == oidctrust.UIComponentValue
}

// CustomizeObjects returns objs with s applied to the ichor-ui Deployment, the controller
// Deployment and the ichor-ui Service; other objects are returned untouched. It runs on the
// installer's copy of the manifest, so the objects are rebuilt rather than merged.
func CustomizeObjects(objs []*unstructured.Unstructured, s UISettings) ([]*unstructured.Unstructured, error) {
	out := make([]*unstructured.Unstructured, len(objs))

	for i, o := range objs {
		out[i] = o

		switch o.GetKind() {
		case kindDeployment:
			dep := &appsv1.Deployment{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, dep); err != nil {
				return nil, fmt.Errorf("reading %s %s: %w", o.GetKind(), o.GetName(), err)
			}

			var err error

			switch {
			case slices.ContainsFunc(dep.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == UIContainer }):
				err = CustomizeUI(dep, s)
			case slices.ContainsFunc(dep.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == ControllerContainer }):
				err = CustomizeController(dep, s)
			default:
				continue
			}

			if err != nil {
				return nil, err
			}

			if out[i], err = toUnstructured(dep, kindDeployment, "apps/v1"); err != nil {
				return nil, err
			}
		case "Service":
			svc := &corev1.Service{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, svc); err != nil {
				return nil, fmt.Errorf("reading %s %s: %w", o.GetKind(), o.GetName(), err)
			}

			if !IsUIService(svc) {
				continue
			}

			CustomizeService(svc, s)

			var err error
			if out[i], err = toUnstructured(svc, "Service", "v1"); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

func toUnstructured(obj any, kind, apiVersion string) (*unstructured.Unstructured, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{Object: m}
	u.SetKind(kind)
	u.SetAPIVersion(apiVersion)
	// The typed round trip adds an empty status and creationTimestamp; the apply must not carry them.
	unstructured.RemoveNestedField(u.Object, "status")
	unstructured.RemoveNestedField(u.Object, "metadata", "creationTimestamp")

	return u, nil
}

// customizeFromLive reads the live ichor-ui and controller Deployments the manifest names and
// customizes the manifest's objects with the settings they carry. A Deployment that does not
// exist yet (a first install by the installer) reads as a plain install.
func customizeFromLive(ctx context.Context, c client.Client, objs []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	live := func(o *unstructured.Unstructured) (*appsv1.Deployment, error) {
		d := &appsv1.Deployment{}

		err := c.Get(ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: o.GetName()}, d)

		switch {
		case apierrors.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, fmt.Errorf("reading live Deployment %s: %w", o.GetName(), err)
		}

		return d, nil
	}

	var uiDep, ctrlDep *appsv1.Deployment

	for _, o := range objs {
		if o.GetKind() != kindDeployment {
			continue
		}

		d := &appsv1.Deployment{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, d); err != nil {
			return nil, err
		}

		var err error

		switch {
		case slices.ContainsFunc(d.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == UIContainer }):
			uiDep, err = live(o)
		case slices.ContainsFunc(d.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == ControllerContainer }):
			ctrlDep, err = live(o)
		}

		if err != nil {
			return nil, err
		}
	}

	s := ReadUISettings(uiDep, ctrlDep)

	return CustomizeObjects(objs, s)
}
