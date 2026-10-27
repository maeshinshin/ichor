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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// `ichor init --ui-url https://... [--admin-user NAME]`.
//
// init installs Ichor from the embedded kustomize manifest, not from the Helm
// chart, so "turning users on" applies in place what ichorUI.users.enabled
// renders into the chart: the flags, port and Secret volumes of the ichor-ui
// Deployment, the internal port of its Service, and the credentials Secret.
// The kube-apiserver side is spec.authentication.ichor on the TalosCluster.

const (
	// initUsersMinContract is the first Talos contract with KubeAuthenticationConfig.
	initUsersMinContract = "v1.14"
	defaultAdminUser     = "admin"
	uiDeploymentName     = "ichor-ui"
	uiServiceName        = "ichor-ui-service"
	uiInternalPort       = 8444
	uiInternalPortName   = "internal"
	schemeHTTPS          = "https"
	appNameIchor         = "ichor"
)

// validateInitUsers runs before anything irreversible, so a bad URL or an old
// contract costs nothing.
func prepareInitUsers(opts initOptions) (initOptions, error) {
	if opts.UIURL != "" && opts.AdminUser == "" {
		opts.AdminUser = defaultAdminUser
	}

	return opts, validateInitUsers(opts)
}

func validateInitUsers(opts initOptions) error {
	if opts.UIURL == "" {
		if opts.AdminUser != "" {
			return errors.New("init: --admin-user needs --ui-url (without it init turns users off and creates none)")
		}

		return nil
	}

	u, err := url.Parse(opts.UIURL)
	if err != nil || u.Scheme != schemeHTTPS || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("init: --ui-url %q must be the https URL of ichor-ui, without query, fragment or credentials", opts.UIURL)
	}

	if semver.Compare(opts.VersionContract, initUsersMinContract) < 0 {
		return fmt.Errorf("init: --ui-url needs --version-contract %s or later (the kube-apiserver can trust Ichor's issuer only through "+
			"Talos's KubeAuthenticationConfig); %s is older", initUsersMinContract, opts.VersionContract)
	}

	return errors.Join(ichorauth.ValidateUserName(opts.AdminUser))
}

// ensureCredentialsSecret creates the empty store of password hashes ichor-ui's
// ServiceAccount may patch but cannot create. An existing Secret is kept.
func ensureCredentialsSecret(ctx context.Context, k client.Client, ns string) error {
	sec := &corev1.Secret{
		Namespace: ns, Name: ichorauth.CredentialsSecretName,
		Labels: map[string]string{"app.kubernetes.io/name": appNameIchor, "app.kubernetes.io/component": "ui"},
		Type:   corev1.SecretTypeOpaque,
	}

	err := retryNoMatch(ctx, func() error { return k.Create(ctx, sec) })
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("init: creating the credentials Secret: %w", err)
	}

	return nil
}

// enableUIUsers is the in-place equivalent of the chart's ichorUI.users.enabled
// for the ichor-ui Deployment and Service.
func enableUIUsers(ctx context.Context, k client.Client, ns, externalURL string) error {
	var dep appsv1.Deployment
	if err := k.Get(ctx, types.NamespacedName{Namespace: ns, Name: uiDeploymentName}, &dep); err != nil {
		return fmt.Errorf("init: reading the ichor-ui Deployment: %w", err)
	}

	if err := usersOnDeployment(&dep, externalURL); err != nil {
		return err
	}

	if err := k.Update(ctx, &dep); err != nil {
		return fmt.Errorf("init: updating the ichor-ui Deployment: %w", err)
	}

	var svc corev1.Service
	if err := k.Get(ctx, types.NamespacedName{Namespace: ns, Name: uiServiceName}, &svc); err != nil {
		return fmt.Errorf("init: reading the ichor-ui Service: %w", err)
	}

	if !slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool { return p.Name == uiInternalPortName }) {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name: uiInternalPortName, Port: uiInternalPort, TargetPort: intstr.FromInt32(uiInternalPort), Protocol: corev1.ProtocolTCP,
		})
	}

	if err := k.Update(ctx, &svc); err != nil {
		return fmt.Errorf("init: updating the ichor-ui Service: %w", err)
	}

	return nil
}

func usersOnDeployment(dep *appsv1.Deployment, externalURL string) error {
	i := slices.IndexFunc(dep.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == uiDeploymentName })
	if i < 0 {
		return fmt.Errorf("init: the ichor-ui Deployment has no container %q", uiDeploymentName)
	}

	c := &dep.Spec.Template.Spec.Containers[i]

	// Drop any earlier setting of the same flags so a re-run replaces the URL.
	c.Args = slices.DeleteFunc(c.Args, func(a string) bool {
		return a == "--users-enabled=true" || strings.HasPrefix(a, "--external-url=")
	})
	c.Args = append(c.Args, "--users-enabled=true", "--external-url="+externalURL)

	if !slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool { return p.Name == uiInternalPortName }) {
		c.Ports = append(c.Ports, corev1.ContainerPort{Name: uiInternalPortName, ContainerPort: uiInternalPort, Protocol: corev1.ProtocolTCP})
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

	return nil
}

// setClusterAuthentication turns on Ichor's issuer in the kube-apiserver
// (spec.authentication.ichor). It is a merge patch after the cluster exists, so
// the first configuration is the plain one and the rollout is a separate step.
func setClusterAuthentication(ctx context.Context, k client.Client, cluster *ichorv1alpha1.TalosCluster, externalURL string) error {
	if externalURL == "" {
		return nil
	}

	type specPatch struct {
		Authentication *ichorv1alpha1.TalosClusterAuthentication `json:"authentication"`
	}

	patch, err := json.Marshal(struct {
		Spec specPatch `json:"spec"`
	}{specPatch{&ichorv1alpha1.TalosClusterAuthentication{Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: externalURL}}}})
	if err != nil {
		return err
	}

	if err := k.Patch(ctx, cluster, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return fmt.Errorf("init: setting spec.authentication.ichor on TalosCluster %q: %w", cluster.Name, err)
	}

	return nil
}

// initUsers runs the --ui-url half of init after Ichor is installed and before
// the TalosCluster exists: credentials Secret, first admin (before the switch,
// so nobody is locked out), then the switch. It returns the generated password.
func initUsers(ctx context.Context, opts initOptions, k client.Client, errw io.Writer) (password string, err error) {
	if opts.UIURL == "" {
		return "", nil
	}

	if err := ensureCredentialsSecret(ctx, k, ichorControllerNamespace); err != nil {
		return "", err
	}

	password, err = ichorauth.GeneratePassword()
	if err != nil {
		return "", fmt.Errorf("init: generating the admin password: %w", err)
	}

	spec := userSpec{Name: opts.AdminUser, DisplayName: opts.AdminUser, Roles: []string{ichorauth.RoleAdmin}}
	if err := createUserWithPassword(ctx, k, spec, password, time.Now()); err != nil {
		return "", fmt.Errorf("init: creating the first admin: %w", err)
	}

	if err := enableUIUsers(ctx, k, ichorControllerNamespace, opts.UIURL); err != nil {
		return password, err
	}

	_, _ = fmt.Fprintf(errw, "init: users are on in ichor-ui (issuer %s); first admin %q created\n", opts.UIURL, opts.AdminUser)

	return password, nil
}

// initUsersEpilogue is what the operator has to do next. It prints the
// generated password, once, to stderr.
func initUsersEpilogue(errw io.Writer, opts initOptions, password string) {
	if opts.UIURL == "" {
		return
	}

	_, _ = fmt.Fprintf(errw, "init: Helm/GitOps equivalent: ichorUI.users.enabled=true, ichorUI.users.externalURL=%s\n", opts.UIURL)
	_, _ = fmt.Fprintf(errw, "init: make ichor-ui reachable at %s over https (an Ingress, Gateway or LoadBalancer in front of "+
		"service %s, or ichorUI.tls.secretName); Ichor installs none\n", opts.UIURL, uiServiceName)
	_, _ = fmt.Fprintln(errw, "init: spec.authentication.ichor is set; the controller renders the kube-apiserver authentication "+
		"config next, which restarts kube-apiserver on each control plane. If a change waits for a reboot, run `ichor config apply`")

	if password != "" {
		printGeneratedPassword(errw, opts.AdminUser, password)
	}
}
