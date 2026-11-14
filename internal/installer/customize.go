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
	"log"
	"net"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
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
	argAddr            = "--addr="
	argUIExternalURL   = "--ui-external-url="
	argAllowAnonymous  = "--allow-unauthenticated=true"
	envOIDCIssuer      = "ICHOR_OIDC_ISSUER"

	// UIDefaultAddr is the HTTPS address a fresh install serves.
	UIDefaultAddr = ":8443"
	UIPortName    = "https"
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

	// ListenAddr pins ichor-ui's --addr.
	// Empty keeps the manifest's default.
	ListenAddr string

	// WasPlaintext is the address a plaintext install served (plain
	// HTTP is gone). It only drives the upgrade notice; such an install moves to HTTPS on 8443.
	WasPlaintext string

	// RefreshLifetime is the controller's --oidc-refresh-lifetime; empty means the default.
	RefreshLifetime string

	// AllowUnauthenticated sets ichor-ui's --allow-unauthenticated: without a
	// sign-in ichor-ui serves read-only calls only, so an install that has no sign-in and wants
	// to keep writing says so. It is ignored, and the flag removed, when users are on.
	AllowUnauthenticated bool

	// InferredAnonymous marks a live install that ran with no sign-in and no flag, so
	// AllowUnauthenticated was set to keep its behaviour; it only drives the upgrade notice.
	InferredAnonymous bool
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

func hasContainer(dep *appsv1.Deployment, name string) bool {
	_, err := container(dep, name)
	return err == nil
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

			// An install without any sign-in used to be an open write proxy. It keeps
			// that behaviour on upgrade, but now explicitly, with a notice.
			s.AllowUnauthenticated = slices.Contains(c.Args, argAllowAnonymous)
			if !s.AllowUnauthenticated && !s.UsersOn() && !hasOIDC(c) {
				s.AllowUnauthenticated, s.InferredAnonymous = true, true
			}

			// Migration table. An address other than the new default is an install that
			// predates it: with the operator's pair it is pinned, without it the install served
			// plaintext, which is gone (unit 10), so it moves to HTTPS on the default port.
			addr := argValue(c.Args, argAddr)
			old := addr != "" && addr != UIDefaultAddr

			for _, v := range ui.Spec.Template.Spec.Volumes {
				if v.Name == UITLSVolume && v.Secret != nil {
					s.TLSSecretName = v.Secret.SecretName
				}
			}

			switch {
			case s.TLSSecretName != "":
				if old {
					s.ListenAddr = addr
				}
			case old:
				s.WasPlaintext = addr
			case argValue(c.Args, "--http-addr=") != "":
				s.WasPlaintext = argValue(c.Args, "--http-addr=")
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
		return a == "--users-enabled=true" || a == argAllowAnonymous || strings.HasPrefix(a, argExternalURL) || strings.HasPrefix(a, "--internal-jwks-uri=") ||
			strings.HasPrefix(a, argKubeAPIURL) || strings.HasPrefix(a, argKubeAPIName) || strings.HasPrefix(a, "--http-addr=") ||
			(s.ListenAddr != "" && strings.HasPrefix(a, argAddr))
	})

	if s.ListenAddr != "" {
		c.Args = append(c.Args, argAddr+s.ListenAddr)

		if err := setPort(c, UIPortName, s.ListenAddr); err != nil {
			return fmt.Errorf("ichor-ui Deployment %s: %w", dep.Name, err)
		}
	}

	if s.UsersOn() {
		// --internal-jwks-uri is dropped on purpose: ichor-ui answers with the host the apiserver
		// used, so a recreated Service (new ClusterIP) needs no change here.
		c.Args = append(c.Args, "--users-enabled=true", argExternalURL+s.ExternalURL)
		usersVolumes(dep, c)
	}

	if s.AllowUnauthenticated && !s.UsersOn() && !hasOIDC(c) {
		c.Args = append(c.Args, argAllowAnonymous)
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

// hasOIDC reports whether the container is set up for an external OIDC provider.
func hasOIDC(c *corev1.Container) bool {
	if argValue(c.Args, "--oidc-issuer=") != "" {
		return true
	}

	return slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == envOIDCIssuer && e.Value != "" })
}

func usersVolumes(dep *appsv1.Deployment, c *corev1.Container) {
	if !slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool { return p.Name == UIInternalPortName }) {
		c.Ports = append(c.Ports, corev1.ContainerPort{Name: UIInternalPortName, ContainerPort: UIInternalPort, Protocol: corev1.ProtocolTCP})
	}

	// Written by the controller; optional so ichor-ui starts before it has made them.
	for _, v := range []struct{ name, secret, path string }{
		{"oidc-signing", ichorauth.SigningSecretName, "/etc/ichor/oidc-signing"},
		{"oidc-tls", "ichor-oidc-tls", "/etc/ichor/oidc-tls"},
	} {
		if !slices.ContainsFunc(dep.Spec.Template.Spec.Volumes, func(x corev1.Volume) bool { return x.Name == v.name }) {
			dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes, corev1.Volume{
				Name:   v.name,
				Secret: &corev1.SecretVolumeSource{SecretName: v.secret, Optional: ptr.To(true)},
			})
		}

		if !slices.ContainsFunc(c.VolumeMounts, func(x corev1.VolumeMount) bool { return x.Name == v.name }) {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: v.name, MountPath: v.path, ReadOnly: true})
		}
	}
}

func tlsOnUI(dep *appsv1.Deployment, c *corev1.Container, secret string) error {
	addr := argValue(c.Args, argAddr)
	if addr == "" {
		return fmt.Errorf("ichor-ui Deployment %s: no --addr argument to take the listening port from", dep.Name)
	}

	port, err := addrPort(addr)
	if err != nil {
		return fmt.Errorf("ichor-ui Deployment %s: %w", dep.Name, err)
	}

	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return strings.HasPrefix(a, "--tls-cert-file=") || strings.HasPrefix(a, "--tls-key-file=")
	})
	c.Args = append(c.Args, "--tls-cert-file="+UITLSMountPath+"/tls.crt", "--tls-key-file="+UITLSMountPath+"/tls.key")

	// Readiness is an HTTPS GET (a kubelet TCP probe makes a TLS server log a handshake error), so the
	// pod stays out of the Service until the Ichor-made leaf exists. Liveness is a TCP connect on the
	// same port: the listener accepts before any certificate does, and an HTTPS liveness probe would
	// restart the pod while the optional Secret volume is still being populated.
	// Whole probes are replaced: the handler is a oneof, and a tcpSocket left beside an httpGet is invalid.
	c.LivenessProbe = &corev1.Probe{
		TCPSocket:           &corev1.TCPSocketAction{Port: intstr.FromInt32(port)},
		InitialDelaySeconds: 5, PeriodSeconds: 20, TimeoutSeconds: 5,
	}
	c.ReadinessProbe = &corev1.Probe{
		HTTPGet: &corev1.HTTPGetAction{
			Path: "/auth/config", Port: intstr.FromInt32(port), Scheme: corev1.URISchemeHTTPS,
		},
		InitialDelaySeconds: 5, PeriodSeconds: 10, TimeoutSeconds: 5,
	}

	vols := slices.DeleteFunc(dep.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == UITLSVolume })
	dep.Spec.Template.Spec.Volumes = append(vols, corev1.Volume{
		Name: UITLSVolume, Secret: &corev1.SecretVolumeSource{SecretName: secret},
	})

	c.VolumeMounts = slices.DeleteFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == UITLSVolume })
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: UITLSVolume, MountPath: UITLSMountPath, ReadOnly: true})

	return nil
}

// addrPort reads the port of a listen address such as ":8080".
func addrPort(addr string) (int32, error) {
	_, p, err := net.SplitHostPort(addr)

	var n int64
	if err == nil {
		n, err = strconv.ParseInt(p, 10, 32)
	}

	if err != nil {
		return 0, fmt.Errorf("cannot read the port from %q: %w", addr, err)
	}

	return int32(n), nil
}

// setPort makes the container port called name listen on addr's port, adding it if absent.
func setPort(c *corev1.Container, name, addr string) error {
	n, err := addrPort(addr)
	if err != nil {
		return err
	}

	i := slices.IndexFunc(c.Ports, func(p corev1.ContainerPort) bool { return p.Name == name })
	if i < 0 {
		c.Ports = append(c.Ports, corev1.ContainerPort{Name: name, ContainerPort: n, Protocol: corev1.ProtocolTCP})
		return nil
	}

	c.Ports[i].ContainerPort = n

	return nil
}

// CustomizeController adds the flags that make the controller keep the signing keys, internal
// TLS and trust ConfigMap (the chart renders the same two). With users off it removes them.
func CustomizeController(dep *appsv1.Deployment, s UISettings) error {
	c, err := container(dep, ControllerContainer)
	if err != nil {
		return err
	}

	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return a == "--user-issuer" || a == "--user-issuer=true" || strings.HasPrefix(a, argRefreshLifetime) ||
			strings.HasPrefix(a, argUIExternalURL)
	})

	if !s.UsersOn() {
		return nil
	}

	// The controller puts the host in the Ichor serving leaf's SANs.
	c.Args = append(c.Args, argUIExternalURL+s.ExternalURL)

	life := s.RefreshLifetime
	if life == "" {
		life = DefaultRefreshLifetime
	}

	c.Args = append(c.Args, "--user-issuer", argRefreshLifetime+life)

	return nil
}

// CustomizeService adds the internal port users need to the ichor-ui Service.
func CustomizeService(svc *corev1.Service, s UISettings) {
	has := func(name string) int {
		return slices.IndexFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool { return p.Name == name })
	}
	// Migration: the pinned port replaces the new default.
	if p, err := addrPort(s.ListenAddr); err == nil {
		if i := has(UIPortName); i >= 0 {
			svc.Spec.Ports[i].Port, svc.Spec.Ports[i].TargetPort = p, intstr.FromInt32(p)
		}
	}

	if s.UsersOn() && has(UIInternalPortName) < 0 {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{Name: UIInternalPortName, Port: UIInternalPort, TargetPort: intstr.FromInt32(UIInternalPort), Protocol: corev1.ProtocolTCP})
	}
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
			case hasContainer(dep, UIContainer):
				err = CustomizeUI(dep, s)
			case hasContainer(dep, ControllerContainer):
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

			if svc.Labels[oidctrust.UIComponentLabel] != oidctrust.UIComponentValue {
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

func namespaceOf(deps ...*appsv1.Deployment) string {
	for _, d := range deps {
		if d != nil {
			return d.Namespace
		}
	}

	return ""
}

// clusterIssuerURL returns spec.authentication.ichor.url of the one TalosCluster in ns that enables
// Ichor's issuer, or "" when there is none or more than one, or none has a URL. It reads unstructured
// so the installer needs no typed scheme, and a cluster without the CRD yet reads as none.
func clusterIssuerURL(ctx context.Context, c client.Client, ns string) (string, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{Group: "ichor.maesh.dev", Version: "v1alpha1", Kind: "TalosClusterList"})

	if err := c.List(ctx, list, client.InNamespace(ns)); err != nil {
		if meta.IsNoMatchError(err) {
			return "", nil
		}

		return "", fmt.Errorf("listing the TalosClusters in %s: %w", ns, err)
	}

	var urls []string

	for _, tc := range list.Items {
		enabled, _, _ := unstructured.NestedBool(tc.Object, "spec", "authentication", "ichor", "enabled")
		url, _, _ := unstructured.NestedString(tc.Object, "spec", "authentication", "ichor", "url")

		if enabled && url != "" {
			urls = append(urls, url)
		}
	}

	if len(urls) != 1 {
		return "", nil
	}

	return urls[0], nil
}

// customizeFromLive reads the live ichor-ui and controller Deployments the manifest names and
// customizes the manifest's objects with the settings they carry. A Deployment that does not
// exist yet (a first install by the installer) reads as a plain install.
func customizeFromLive(ctx context.Context, c client.Client, objs []*unstructured.Unstructured) ([]*unstructured.Unstructured, UISettings, error) {
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
			return nil, UISettings{}, err
		}

		var err error

		switch {
		case hasContainer(d, UIContainer):
			uiDep, err = live(o)
		case hasContainer(d, ControllerContainer):
			ctrlDep, err = live(o)
		}

		if err != nil {
			return nil, UISettings{}, err
		}
	}

	if helmDeployed(ctrlDep) {
		return nil, UISettings{}, errors.New("this Ichor is installed by Helm; run helm upgrade (the installer does not touch a Helm-owned install)")
	}

	s := ReadUISettings(uiDep, ctrlDep)

	// The issuer is the TalosCluster's spec.authentication.ichor.url, set at init and changeable later,
	// so a URL changed on the cluster reaches the Deployments on the next install or upgrade.
	if ns := namespaceOf(uiDep, ctrlDep); ns != "" {
		url, err := clusterIssuerURL(ctx, c, ns)
		if err != nil {
			return nil, UISettings{}, err
		}

		if url != "" {
			s.ExternalURL = url
		}
	}

	if s.WasPlaintext != "" {
		log.Printf("NOTICE: ichor-ui no longer serves plain HTTP (it did on %s); point your edge at https://<ichor-ui>:8443 (trust ichor-ui-ca)", s.WasPlaintext)
	}

	if s.InferredAnonymous {
		log.Print("NOTICE: this ichor-ui has no sign-in (no Ichor users, no OIDC). It serves read-only calls " +
			"unless started with --allow-unauthenticated; that flag is now set for you, so it keeps working as before. " +
			"Turn on Ichor users or OIDC and drop the flag (docs/guides/users-and-login.md)")
	}

	out, err := CustomizeObjects(objs, s)

	return out, s, err
}

// isUIPolicy: the CiliumNetworkPolicy of ichor-ui in config/cilium-network-policy, found by the
// pods it selects (control-plane: ui-backend) because kustomize's namePrefix renames it (ichor-ui).
func isUIPolicy(o *unstructured.Unstructured) bool {
	v, _, _ := unstructured.NestedString(o.Object, "spec", "endpointSelector", "matchLabels", "control-plane")

	return o.GetKind() == "CiliumNetworkPolicy" && v == "ui-backend"
}

// CustomizePolicies opens, in the ichor-ui CiliumNetworkPolicy, the pinned TLS port of a migrated
// install. The policies list
// 8443 only, which is all a fresh install needs.
func CustomizePolicies(objs []*unstructured.Unstructured, s UISettings) {
	p, err := addrPort(s.ListenAddr)
	if err != nil || p == 8443 {
		return
	}

	extra := map[string]any{"port": strconv.Itoa(int(p)), "protocol": "TCP"}

	for _, o := range objs {
		if !isUIPolicy(o) {
			continue
		}

		ing, _, _ := unstructured.NestedSlice(o.Object, "spec", "ingress")
		if len(ing) == 0 {
			continue
		}

		rule, _ := ing[0].(map[string]any)
		toPorts, _ := rule["toPorts"].([]any)

		if len(toPorts) == 0 {
			continue
		}

		tp, _ := toPorts[0].(map[string]any)
		ports, _ := tp["ports"].([]any)
		tp["ports"] = append(ports, extra)

		_ = unstructured.SetNestedSlice(o.Object, ing, "spec", "ingress")
	}
}

// helmDeployed: the live controller Deployment is Helm's, by its label or by the --deployed-by
// flag the chart passes. The installer Job's ServiceAccount cannot read the IchorInstallation, so
// the Deployment is its second line behind the webhook and the executor.
func helmDeployed(d *appsv1.Deployment) bool {
	if d == nil {
		return false
	}

	if d.Labels["app.kubernetes.io/managed-by"] == "Helm" {
		return true
	}

	c, err := container(d, ControllerContainer)

	return err == nil && slices.Contains(c.Args, "--deployed-by=helm")
}
