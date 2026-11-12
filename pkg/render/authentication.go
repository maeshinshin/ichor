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
	"context"
	"encoding/json"
	"errors"
	"fmt"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	apiserverinternal "k8s.io/apiserver/pkg/apis/apiserver"
	apiserverv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/apiserver/pkg/apis/apiserver/validation"
	authenticationcel "k8s.io/apiserver/pkg/authentication/cel"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The names fixed for Ichor's own issuer.
const (
	// OIDCTrustConfigMap is the ConfigMap the controller publishes the issuer's public trust
	// material in. It lives in the controller's namespace.
	OIDCTrustConfigMap = "ichor-oidc-trust"

	// OIDCTrustKeyDiscoveryURL and OIDCTrustKeyCA are its keys. shortcut: only the two
	// values are fixed, not the keys; these are the render side's reading of them.
	OIDCTrustKeyDiscoveryURL = "discoveryURL"
	OIDCTrustKeyCA           = "ca.crt"

	// IchorKubeAudience is the one audience the kube-apiserver accepts from Ichor's issuer.
	IchorKubeAudience = "ichor-kube"

	// IchorPrefix prefixes Ichor's users and groups; external providers cannot use it.
	IchorPrefix = "ichor:"
)

// OIDCTrust is the public trust material of Ichor's issuer, read from OIDCTrustConfigMap.
type OIDCTrust struct {
	// DiscoveryURL is the in-cluster https URL of the discovery document.
	DiscoveryURL string
	// CAPEM is the PEM of the CA that signed the issuer's internal leaf.
	CAPEM string
}

// resolveOIDCTrust reads OIDCTrustConfigMap when cluster enables Ichor's issuer. A missing
// ConfigMap is not an error here (nil result): the render says so, with the cluster in hand.
func resolveOIDCTrust(
	ctx context.Context, c client.Client, namespace string, cluster *ichorv1alpha1.TalosCluster,
) (*OIDCTrust, error) {
	if !ichorIssuerEnabled(cluster) {
		return nil, nil //nolint:nilnil // nothing to read is the normal case
	}

	if namespace == "" {
		return nil, errors.New("resolving the Ichor issuer's trust: controller namespace is empty (is POD_NAMESPACE set?)")
	}

	var cm corev1.ConfigMap

	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: OIDCTrustConfigMap}, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil //nolint:nilnil // reported by AuthenticationConfiguration
		}

		return nil, fmt.Errorf("reading ConfigMap %s/%s: %w", namespace, OIDCTrustConfigMap, err)
	}

	return &OIDCTrust{DiscoveryURL: cm.Data[OIDCTrustKeyDiscoveryURL], CAPEM: cm.Data[OIDCTrustKeyCA]}, nil
}

func ichorIssuerEnabled(cluster *ichorv1alpha1.TalosCluster) bool {
	a := cluster.Spec.Authentication

	return a != nil && a.Ichor != nil && a.Ichor.Enabled
}

// AuthenticationConfiguration builds the kube-apiserver's AuthenticationConfiguration for
// spec.authentication and validates it with the apiserver's own validation, so an
// invalid file never reaches a node (it would stop every control plane's kube-apiserver). The
// anonymous block is always present: the Talos document's configuration is merge:"replace", and
// without it the health probes would lose anonymous access. trust is required when Ichor's
// issuer is enabled.
func AuthenticationConfiguration(
	spec *ichorv1alpha1.TalosClusterAuthentication, trust *OIDCTrust,
) (*apiserverv1.AuthenticationConfiguration, error) {
	cfg := &apiserverv1.AuthenticationConfiguration{
		JWT: []apiserverv1.JWTAuthenticator{},
		// Talos's default (k8s.DefaultAuthenticationConfig): anonymous only for the three probes.
		Anonymous: &apiserverv1.AnonymousAuthConfig{Enabled: true, Conditions: []apiserverv1.AnonymousAuthCondition{
			{Path: "/livez"}, {Path: "/readyz"}, {Path: "/healthz"},
		}},
	}

	if spec.Ichor != nil && spec.Ichor.Enabled {
		// An empty CAPEM is checked by the caller that renders (injectAuthentication): the webhook
		// validates the spec before the controller has published any CA.
		if trust == nil || trust.DiscoveryURL == "" {
			return nil, fmt.Errorf("spec.authentication.ichor is enabled but ConfigMap %q is missing or has no %s/%s: "+
				"the controller publishes it once Ichor's issuer is set up (ichorUI.users.enabled)",
				OIDCTrustConfigMap, OIDCTrustKeyDiscoveryURL, OIDCTrustKeyCA)
		}

		discovery := trust.DiscoveryURL

		cfg.JWT = append(cfg.JWT, jwtAuthenticator(
			apiserverv1.Issuer{
				URL: spec.Ichor.URL, DiscoveryURL: &discovery, CertificateAuthority: trust.CAPEM,
				Audiences: []string{IchorKubeAudience},
			},
			"sub", IchorPrefix, "groups", IchorPrefix))
	}

	for _, e := range spec.External {
		cfg.JWT = append(cfg.JWT, jwtAuthenticator(
			apiserverv1.Issuer{URL: e.IssuerURL, CertificateAuthority: e.CertificateAuthority, Audiences: e.Audiences},
			e.UsernameClaim, e.UsernamePrefix, e.GroupsClaim, e.GroupsPrefix))
	}

	if err := validateAuthenticationConfiguration(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// jwtAuthenticator is one jwt[] element: the issuer, the claim mappings with their prefixes, and
// the rules that keep a token from naming a system: user or group.
func jwtAuthenticator(
	issuer apiserverv1.Issuer, userClaim, userPrefix, groupsClaim, groupsPrefix string,
) apiserverv1.JWTAuthenticator {
	// More than one audience needs audienceMatchPolicy MatchAny, or kube-apiserver refuses the file
	// and crash-loops (hardware spike): Talos does not validate the document.
	if len(issuer.Audiences) > 1 {
		issuer.AudienceMatchPolicy = apiserverv1.AudienceMatchPolicyMatchAny
	}

	return apiserverv1.JWTAuthenticator{
		Issuer: issuer,
		ClaimMappings: apiserverv1.ClaimMappings{
			Username: apiserverv1.PrefixedClaimOrExpression{Claim: userClaim, Prefix: &userPrefix},
			Groups:   apiserverv1.PrefixedClaimOrExpression{Claim: groupsClaim, Prefix: &groupsPrefix},
		},
		UserValidationRules: []apiserverv1.UserValidationRule{
			{Expression: "!user.username.startsWith('system:')", Message: "username cannot use reserved system: prefix"},
			{
				Expression: "!user.groups.exists(g, g.startsWith('system:'))",
				Message:    "groups cannot use reserved system: prefix",
			},
		},
	}
}

// validateAuthenticationConfiguration converts cfg to the apiserver's internal type and runs
// validation.ValidateAuthenticationConfiguration on it.
func validateAuthenticationConfiguration(cfg *apiserverv1.AuthenticationConfiguration) error {
	var internal apiserverinternal.AuthenticationConfiguration

	convert := apiserverv1.Convert_v1_AuthenticationConfiguration_To_apiserver_AuthenticationConfiguration
	if err := convert(cfg, &internal, nil); err != nil {
		return fmt.Errorf("converting the authentication configuration: %w", err)
	}

	// No disallowed issuers: the service-account issuer is the control-plane endpoint, which no
	// OIDC provider URL here can be in practice. shortcut: pass it if that ever bites.
	compiler := authenticationcel.NewDefaultCompiler()
	if errs := validation.ValidateAuthenticationConfiguration(compiler, &internal, nil); len(errs) > 0 {
		return fmt.Errorf("the authentication configuration is invalid: %w", errs.ToAggregate())
	}

	return nil
}

// ValidateAuthentication is the admission-time check of spec.authentication: the
// version contract must be v1.14 or later, and the configuration it would render must pass the
// apiserver's own validation. Ichor's trust material does not exist yet when the spec is written,
// so a placeholder discovery URL stands in for it (the real one is validated again at render).
func ValidateAuthentication(cluster *ichorv1alpha1.TalosCluster) error {
	spec := cluster.Spec.Authentication
	if spec == nil || (!ichorIssuerEnabled(cluster) && len(spec.External) == 0) {
		return nil
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return fmt.Errorf("parsing versionContract: %w", err)
	}

	if !contract.MultidocKubernetesConfigSupported() {
		return fmt.Errorf("spec.authentication needs versionContract v1.14 or later (Talos has the typed "+
			"KubeAuthenticationConfig document only from then), the cluster has %s", cluster.Spec.VersionContract)
	}

	placeholder := &OIDCTrust{DiscoveryURL: "https://ichor-ui-service.ichor-system.svc:8444" +
		"/.well-known/openid-configuration"}
	_, err = AuthenticationConfiguration(spec, placeholder)

	return err
}

// injectAuthentication renders spec.authentication onto control-plane nodes as the
// typed KubeAuthenticationConfig document, applied as a strategic merge patch over the document
// Talos generates (empty jwt). It is a system-layer step; unset, or nothing enabled, returns cfg
// untouched. Talos only has the document from contract v1.14 (earlier contracts have the single
// legacy --oidc-* flag set, which Ichor does not render), so a lower contract is an error.
func injectAuthentication(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract,
	cluster *ichorv1alpha1.TalosCluster, machineType machine.Type, sys *SystemInputs,
) (coreconfig.Provider, error) {
	spec := cluster.Spec.Authentication
	nothing := spec == nil || (!ichorIssuerEnabled(cluster) && len(spec.External) == 0)
	if nothing || machineType != machine.TypeControlPlane {
		return cfg, nil
	}

	if !contract.MultidocKubernetesConfigSupported() {
		return nil, fmt.Errorf("render: spec.authentication needs versionContract v1.14 or later, the cluster has %s",
			cluster.Spec.VersionContract)
	}

	trust := sys.OIDCTrust
	if ichorIssuerEnabled(cluster) && (trust == nil || trust.CAPEM == "") {
		return nil, fmt.Errorf("render: spec.authentication.ichor is enabled but ConfigMap %q is missing or has no %s/%s: "+
			"the controller publishes it once Ichor's issuer is set up (ichorUI.users.enabled)",
			OIDCTrustConfigMap, OIDCTrustKeyDiscoveryURL, OIDCTrustKeyCA)
	}

	ac, err := AuthenticationConfiguration(spec, trust)
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	obj, err := toUnstructured(ac)
	if err != nil {
		return nil, fmt.Errorf("render: authentication configuration: %w", err)
	}

	doc := k8s.NewKubeAuthenticationConfigV1Alpha1()
	doc.AuthConfig = mmeta.Unstructured{Object: obj}

	return mergeDocument(cfg, "authentication configuration", doc)
}

// toUnstructured is the JSON round trip every typed Kubernetes block takes into a Talos
// meta.Unstructured (typedconfig's mirrorToMachineryUnstructured does the same but is
// unexported). Talos adds apiVersion and kind itself when it writes the file.
func toUnstructured(ac *apiserverv1.AuthenticationConfiguration) (map[string]any, error) {
	data, err := json.Marshal(ac)
	if err != nil {
		return nil, err
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}

	delete(obj, "apiVersion")
	delete(obj, "kind")

	return obj, nil
}
