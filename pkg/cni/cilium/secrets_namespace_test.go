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

package cilium

import (
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// secretsNamespaceKeys are the chart values that decide the namespace of the Roles that give the
// Cilium ServiceAccounts read access to Secrets (ADR 0131).
var secretsNamespaceKeys = []string{
	"ingressController.secretsNamespace.name",
	"gatewayAPI.secretsNamespace.name",
	"envoyConfig.secretsNamespace.name",
	"bgpControlPlane.secretsNamespace.name",
	"tls.secretsNamespace.name",
}

// A user value for a secretsNamespace name is refused, whatever it is, so the Roles named
// cilium-*-secrets can only land in Cilium's own namespace.
func TestValidateValues_SecretsNamespacesArePinned(t *testing.T) {
	for _, key := range secretsNamespaceKeys {
		for _, v := range []any{"ichor-system", ciliumSecretsNamespace, "", nil} {
			user := map[string]any{}
			values.Set(user, key, v)

			err := ValidateValues(user, ValidationContext{KubeProxy: cni.KubeProxyEnabled})
			if err == nil || !strings.Contains(err.Error(), key+" is set by Ichor") {
				t.Errorf("%s = %#v: err = %v, want %q is set by Ichor", key, v, err, key)
			}
		}
	}
}

// The effective values put every secretsNamespace name in cilium-secrets, bgpControlPlane included
// (the chart's own default for it is kube-system).
func TestEffectiveValues_SecretsNamespacesAreCiliumsOwn(t *testing.T) {
	user := map[string]any{"bgpControlPlane": map[string]any{keyEnabled: true}}

	eff, err := EffectiveValues(user, cni.KubeProxyEnabled, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range secretsNamespaceKeys {
		if got, _ := values.Get(eff, key); got != ciliumSecretsNamespace {
			t.Errorf("%s = %v, want cilium-secrets", key, got)
		}
	}
}

// Taking over a Cilium whose BGP control plane is on with Secrets in kube-system is reported, because
// Ichor moves the namespace; with BGP off the chart's default is no conflict.
func TestOwnedKeyConflicts_SecretsNamespaceOfAFeatureThatIsOn(t *testing.T) {
	const key = "bgpControlPlane.secretsNamespace.name"

	live := map[string]any{}
	values.Set(live, key, "kube-system")

	cs, err := OwnedKeyConflicts(live, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cs {
		if c.Key == key {
			t.Errorf("BGP off: %v", c)
		}
	}

	values.Set(live, "bgpControlPlane.enabled", true)

	cs, err = OwnedKeyConflicts(live, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	if findConflict(cs, key) == nil {
		t.Errorf("BGP on with kube-system: no conflict in %v", cs)
	}
}

// Rendering the chart with every feature that makes a Role for Secrets turned on leaves none of them
// outside cilium-secrets.
func TestBootstrapManifest_SecretsRolesStayInCiliumsNamespace(t *testing.T) {
	user := map[string]any{}
	for _, f := range []string{"ingressController", "gatewayAPI", "envoyConfig", "bgpControlPlane"} {
		user[f] = map[string]any{keyEnabled: true}
	}

	_, objs := renderObjects(t, New().WithValues(user), cni.KubeProxyReplaced)

	var roles int

	for _, o := range objs {
		if (o.Kind != "Role" && o.Kind != "RoleBinding") || !strings.Contains(o.Metadata.Name, "secrets") {
			continue
		}

		roles++

		if o.Metadata.Namespace != ciliumSecretsNamespace {
			t.Errorf("%s %q is in namespace %q, want cilium-secrets", o.Kind, o.Metadata.Name, o.Metadata.Namespace)
		}
	}

	if roles < 10 {
		t.Errorf("rendered %d Secrets Roles and RoleBindings, want at least 10 (the check would prove nothing)", roles)
	}
}
