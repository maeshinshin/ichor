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
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// secretsNamespaceKeys are the chart values that decide the namespace of the Roles that give the
// Cilium ServiceAccounts read access to Secrets.
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
	for _, f := range []string{"ingressController", keyGatewayAPI, "envoyConfig", "bgpControlPlane"} {
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

// A user value for either key is refused, whatever it is, so the agent never gets a cluster-wide
// Secret read (a compromised node would expose ichor-system).
func TestValidateValues_ClusterWideSecretReadIsPinned(t *testing.T) {
	// The chart schema takes a boolean for the flag and a string for the alias, and is checked first.
	for key, vals := range map[string][]any{
		keyReadSecretsOnly: {true, false, nil},
		keySecretsBackend:  {"local", "k8s", nil},
	} {
		for _, v := range vals {
			user := map[string]any{}
			values.Set(user, key, v)

			err := ValidateValues(user, ValidationContext{KubeProxy: cni.KubeProxyEnabled})
			if err == nil || !strings.Contains(err.Error(), key+" is set by Ichor") {
				t.Errorf("%s = %#v: err = %v, want %q is set by Ichor", key, v, err, key)
			}
		}
	}
}

// The effective values carry true explicitly, not the chart's default.
func TestEffectiveValues_ReadSecretsOnlyFromSecretsNamespace(t *testing.T) {
	eff, err := EffectiveValues(nil, cni.KubeProxyEnabled, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := values.Get(eff, keyReadSecretsOnly); got != true {
		t.Errorf("%s = %#v, want true", keyReadSecretsOnly, got)
	}
}

// The rendered agent ClusterRole holds no get, list or watch on Secrets, with or without the features
// whose own Roles read Secrets.
func TestBootstrapManifest_AgentClusterRoleHasNoClusterWideSecrets(t *testing.T) {
	for _, features := range [][]string{nil, {"ingressController", "gatewayAPI", "envoyConfig", "bgpControlPlane"}} {
		user := map[string]any{}
		for _, f := range features {
			user[f] = map[string]any{keyEnabled: true}
		}

		_, objs := renderObjects(t, New().WithValues(user), cni.KubeProxyReplaced)

		role := find(objs, "ClusterRole", "cilium")
		if role == nil || len(role.Rules) == 0 {
			t.Fatalf("features %v: no cilium ClusterRole with rules", features)
		}

		var nodes bool

		for _, r := range role.Rules {
			if slices.Contains(r.Resources, "nodes") {
				nodes = true
			}

			if slices.Contains(r.Resources, "secrets") {
				t.Errorf("features %v: cilium ClusterRole rule on secrets: %v", features, r)
			}
		}

		if !nodes {
			t.Errorf("features %v: the ClusterRole has no rule on nodes, so it is not the agent's", features)
		}
	}
}

// A running Cilium that reads Secrets cluster-wide is reported, from the live cilium-config (the chart
// writes the key only while the flag is true) and from a release whose values set either key.
func TestOwnedKeyConflicts_ClusterWideSecretRead(t *testing.T) {
	// Chart defaults: the chart's helper treats null as true, so no conflict.
	defaults, err := LiveValuesFromUser(nil)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := OwnedKeyConflicts(defaults, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{keyReadSecretsOnly, keySecretsBackend} {
		if c := findConflict(cs, key); c != nil {
			t.Errorf("chart defaults: %v", c)
		}
	}

	// A live cilium-config with the key reads true; without it, false.
	fromConfigMap := func(data map[string]string) map[string]any {
		live, err := LiveValuesFromCluster(t.Context(), fake.NewSimpleClientset(&corev1.ConfigMap{
			Namespace: ReleaseNamespace, Name: configMapName, Data: data,
		}), nil)
		if err != nil {
			t.Fatal(err)
		}

		return live
	}

	cs, _ = OwnedKeyConflicts(fromConfigMap(map[string]string{cmReadSecretsOnly: valTrue}), cni.KubeProxyEnabled, "")
	if c := findConflict(cs, keyReadSecretsOnly); c != nil {
		t.Errorf("cilium-config with %s: %v", cmReadSecretsOnly, c)
	}

	cs, _ = OwnedKeyConflicts(fromConfigMap(map[string]string{keyIPAM: modeKubernetes}), cni.KubeProxyEnabled, "")
	if findConflict(cs, keyReadSecretsOnly) == nil {
		t.Errorf("cilium-config without %s: no conflict in %v", cmReadSecretsOnly, cs)
	}

	// A release whose values set the flag to false, or the alias to k8s.
	for key, v := range map[string]any{keyReadSecretsOnly: false, keySecretsBackend: "k8s"} {
		user := map[string]any{}
		values.Set(user, key, v)

		live, err := LiveValuesFromUser(user)
		if err != nil {
			t.Fatal(err)
		}

		cs, _ = OwnedKeyConflicts(live, cni.KubeProxyEnabled, "")
		if findConflict(cs, key) == nil {
			t.Errorf("%s = %v: no conflict in %v", key, v, cs)
		}
	}
}
