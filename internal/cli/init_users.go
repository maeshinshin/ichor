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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"golang.org/x/mod/semver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
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
	// defaultUIURL is the issuer when --ui-url is left out: the address `ichor ui` serves ichor-ui on
	// (its port-forward uses the Service's port, uiHTTPSPort).
	defaultUIURL = "https://localhost:8443"
	// Container names are the same on every install path; object names are not (kustomize
	// prefixes them), so objects are found by label.
	uiContainer          = installer.UIContainer
	controllerContainer  = installer.ControllerContainer
	controllerLabel      = "control-plane"
	controllerLabelValue = "controller-manager"
	schemeHTTPS          = "https"
	appNameIchor         = "ichor"
)

// uiSettings is what init customizes, in the form the installer re-derives on every upgrade.
// The customizing functions are the installer package's, so the two cannot drift.
func (o initOptions) uiSettings() installer.UISettings {
	s := installer.UISettings{
		ExternalURL: o.UIURL, KubeAPIServerURL: o.KubeAPIServerURL, KubeAPITLSServerName: o.KubeAPITLSServerName,
	}
	if o.uiTLSOn() {
		s.TLSSecretName = o.uiTLSSecretName()
	}

	return s
}

// signInOn reports whether init turns Ichor's own sign-in on: the default, unless --allow-unauthenticated.
func (o initOptions) signInOn() bool { return !o.AllowUnauthenticated }

// prepareInitUsers fills in the sign-in defaults and then validates them. It runs before anything
// irreversible, so a bad URL or an old contract costs nothing.
func prepareInitUsers(opts initOptions) (initOptions, error) {
	if opts.signInOn() {
		opts.UIURL = cmp.Or(opts.UIURL, defaultUIURL)
		opts.AdminUser = cmp.Or(opts.AdminUser, defaultAdminUser)
	}

	return opts, validateInitUsers(opts)
}

func validateInitUsers(opts initOptions) error {
	if err := validateInitUIExtras(opts); err != nil {
		return err
	}

	if !opts.signInOn() {
		if opts.UIURL != "" {
			return errors.New("init: --ui-url turns sign-in on and --allow-unauthenticated turns it off; give one of them")
		}

		if opts.AdminUser != "" {
			return errors.New("init: --admin-user needs sign-in, and --allow-unauthenticated turns it off (it creates no admin)")
		}

		return nil
	}

	u, err := url.Parse(opts.UIURL)
	if err != nil || u.Scheme != schemeHTTPS || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("init: --ui-url %q must be the https URL of ichor-ui, without query, fragment or credentials", opts.UIURL)
	}

	if semver.Compare(opts.VersionContract, initUsersMinContract) < 0 {
		return fmt.Errorf("init: sign-in is on by default and needs --version-contract %s or later (the kube-apiserver can trust Ichor's issuer "+
			"only through Talos's KubeAuthenticationConfig); %s is older. Pass --allow-unauthenticated to init without sign-in", initUsersMinContract, opts.VersionContract)
	}

	return errors.Join(ichorauth.ValidateUserName(opts.AdminUser))
}

// ensureCredentialsSecret creates the empty store of password hashes ichor-ui's
// ServiceAccount may patch but cannot create. An existing Secret is kept.
func ensureCredentialsSecret(ctx context.Context, k client.Client, ns string, progress io.Writer) error {
	sec := &corev1.Secret{
		Namespace: ns, Name: ichorauth.CredentialsSecretName,
		Labels: map[string]string{"app.kubernetes.io/name": appNameIchor, "app.kubernetes.io/component": "ui"},
		Type:   corev1.SecretTypeOpaque,
	}

	err := createRetrying(ctx, k, sec, progress)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("init: creating the credentials Secret: %w", err)
	}

	return nil
}

// uiTargets are the three objects turning users on edits, already modified in
// memory. They are found by label and built before anything is written, so a
// missing or odd object fails init before the admin user exists.
type uiTargets struct {
	ui, controller *appsv1.Deployment
	svc            *corev1.Service

	// The edits made to each object, kept so a retried Update can redo them on a fresh copy.
	uiMut, ctrlMut func(*appsv1.Deployment) error
	svcMut         func(*corev1.Service) error
}

// findDeploymentByLabel returns the first Deployment in ns with label key=value.
func findDeploymentByLabel(ctx context.Context, k client.Reader, ns, key, value string) (*appsv1.Deployment, error) {
	var list appsv1.DeploymentList
	if err := k.List(ctx, &list, client.InNamespace(ns), client.MatchingLabels{key: value}); err != nil {
		return nil, fmt.Errorf("init: listing Deployments labelled %s=%s in %s: %w", key, value, ns, err)
	}

	if len(list.Items) == 0 {
		return nil, fmt.Errorf("init: no Deployment labelled %s=%s in namespace %s", key, value, ns)
	}

	return &list.Items[0], nil
}

// prepareUITargets finds and edits (in memory) the ichor-ui Deployment and
// Service and the controller Deployment: the in-place equivalent of the
// chart's ichorUI.users.enabled.
func prepareUITargets(ctx context.Context, k client.Client, ns string, s installer.UISettings) (*uiTargets, error) {
	ui, err := findDeploymentByLabel(ctx, k, ns, uiComponentLabel, uiComponentValue)
	if err != nil {
		return nil, err
	}

	mgr, err := findDeploymentByLabel(ctx, k, ns, controllerLabel, controllerLabelValue)
	if err != nil {
		return nil, err
	}

	svc, err := findUIService(ctx, k, ns)
	if err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}

	t := &uiTargets{ui: ui, controller: mgr, svc: svc}
	t.uiMut = func(d *appsv1.Deployment) error { return installer.CustomizeUI(d, s) }
	t.ctrlMut = func(d *appsv1.Deployment) error { return installer.CustomizeController(d, s) }
	t.svcMut = func(v *corev1.Service) error { installer.CustomizeService(v, s); return nil }

	if err := t.uiMut(ui); err != nil {
		return nil, err
	}

	if err := t.ctrlMut(mgr); err != nil {
		return nil, err
	}

	_ = t.svcMut(svc)

	return t, nil
}

func (t *uiTargets) apply(ctx context.Context, k client.Client, progress io.Writer) error {
	if err := updateRetrying(ctx, k, t.ui, t.uiMut, progress); err != nil {
		return fmt.Errorf("init: updating the ichor-ui Deployment: %w", err)
	}

	if err := updateRetrying(ctx, k, t.svc, t.svcMut, progress); err != nil {
		return fmt.Errorf("init: updating the ichor-ui Service: %w", err)
	}

	if err := updateRetrying(ctx, k, t.controller, t.ctrlMut, progress); err != nil {
		return fmt.Errorf("init: updating the controller Deployment: %w", err)
	}

	return nil
}

// setClusterAuthentication turns on Ichor's issuer in the kube-apiserver
// (spec.authentication.ichor). It is a merge patch after the cluster exists, so
// the first configuration is the plain one and the rollout is a separate step.
func setClusterAuthentication(
	ctx context.Context, k client.Client, cluster *ichorv1alpha1.TalosCluster, externalURL string, progress io.Writer,
) error {
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

	err = retryInit(ctx, progress, func() error { return k.Patch(ctx, cluster, client.RawPatch(types.MergePatchType, patch)) })
	if err != nil {
		return fmt.Errorf("init: setting spec.authentication.ichor on TalosCluster %q: %w", cluster.Name, err)
	}

	return nil
}

// initUsers runs the --ui-url half of init after Ichor is installed and before
// the TalosCluster exists: credentials Secret, first admin (before the switch,
// so nobody is locked out), then the switch. It returns the generated password.
func initUsers(ctx context.Context, opts initOptions, k client.Client, errw io.Writer) (password string, err error) {
	if !opts.signInOn() {
		return "", nil
	}

	// Everything that can fail on the cluster's shape is checked before the first write.
	targets, err := prepareUITargets(ctx, k, ichorControllerNamespace, opts.uiSettings())
	if err != nil {
		return "", err
	}

	if err := checkUITLSSecret(ctx, k, ichorControllerNamespace, opts); err != nil {
		return "", err
	}

	if err := storeUITLSSecret(ctx, k, ichorControllerNamespace, opts, errw); err != nil {
		return "", err
	}

	if err := ensureCredentialsSecret(ctx, k, ichorControllerNamespace, errw); err != nil {
		return "", err
	}

	password, err = ichorauth.GeneratePassword()
	if err != nil {
		return "", fmt.Errorf("init: generating the admin password: %w", err)
	}

	spec := userSpec{Name: opts.AdminUser, DisplayName: opts.AdminUser, Roles: []string{ichorauth.RoleAdmin}}
	if err := createUserWithPassword(ctx, k, spec, password, time.Now(), errw); err != nil {
		return "", fmt.Errorf("init: creating the first admin: %w", err)
	}

	if err := targets.apply(ctx, k, errw); err != nil {
		return password, err
	}

	_, _ = fmt.Fprintf(errw, "init: users are on in ichor-ui (issuer %s); first admin %q created\n", opts.UIURL, opts.AdminUser)

	return password, nil
}

// initEpilogue ends init's output: on success how long it took, then what the operator has to do
// (a warning when ichor-ui has no sign-in; with --ui-url the steps that follow, and the generated
// password, once, verbatim), then the commands to run next. A failed run prints no total and no
// next steps, but still the password: by then the user exists and nothing else could show it.
func initEpilogue(p *progress, opts initOptions, password string, ok bool) {
	if opts.DryRun {
		return // a dry run has nothing to hand over
	}

	if ok {
		p.Done()
	}

	if !opts.signInOn() {
		p.Warn("ichor-ui has no sign-in (--allow-unauthenticated): anyone who can reach ichor-ui can change the cluster " +
			"with ichor-ui's permissions, including resetting nodes. Sign-in is the default; leave the flag out.")
	} else {
		p.Info(fmt.Sprintf("Helm equivalent: ichorUI.users.enabled=true, ichorUI.users.externalURL=%s", opts.UIURL))
		if opts.UIURL != defaultUIURL {
			p.Warn(fmt.Sprintf("make ichor-ui reachable at %s over https (an Ingress, Gateway or LoadBalancer in front of "+
				"the ichor-ui Service (label %s=%s), or ichorUI.tls.secretName); Ichor installs none", opts.UIURL, uiComponentLabel, uiComponentValue))
		}
		p.Warn("spec.authentication.ichor is set; the controller renders the kube-apiserver authentication " +
			"config next. Applying it restarts kube-apiserver on each control plane, so nodes report APIServerRestartRequired " +
			"until you run `ichor rollout config` (or Apply in the Overview page's attention list), which rolls one control plane at a time")

		if password != "" {
			printGeneratedPassword(p, opts.AdminUser, password)
		}
	}

	if ok {
		cmds := []string{"ichor get cluster " + opts.Cluster, "ichor get nodes", "ichor ui"}

		if opts.mergedContext != "" {
			p.Info(fmt.Sprintf("kubectl and ichor now use context %s", opts.mergedContext))
		} else {
			p.Info("kubectl and ichor need the new cluster's kubeconfig; `ichor init --merge-kubeconfig` merges it into yours")

			cmds = append([]string{"export KUBECONFIG=~/.ichor/clusters/" + opts.Cluster + "/" + kubeconfigFilename}, cmds...)
		}

		p.Next(cmds...)
	}
}
