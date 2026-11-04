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
	"slices"
	"strings"
	"testing"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

var bothKeys = []string{secretsbundle.TargetServiceAccount, secretsbundle.TargetSecretbox}

const legacyAESCBC = "legacy-aescbc-secret"

// secretboxProviders returns, in order, the secret of the first key of every provider of the rendered
// kube-apiserver EncryptionConfiguration.
func secretboxProviders(t *testing.T, cfg coreconfig.Provider) []string {
	t.Helper()

	obj := cfg.K8sEtcdEncryptionConfig().EtcdEncryptionConfig()

	resources, _ := obj["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("want one resources entry, got %d", len(resources))
	}

	providers, _ := resources[0].(map[string]any)["providers"].([]any)

	out := make([]string, 0, len(providers))

	for _, p := range providers {
		box, _ := p.(map[string]any)["secretbox"].(map[string]any)
		keys, _ := box["keys"].([]any)

		if len(keys) != 1 {
			t.Fatalf("want one key per secretbox provider, got %d", len(keys))
		}

		key, _ := keys[0].(map[string]any)
		if key["name"] == "" {
			t.Fatal("a secretbox key without a name")
		}

		out = append(out, key["secret"].(string))
	}

	return out
}

// publicKeys returns the service-account public keys the API server verifies with, in the order it is given them.
func publicKeys(cfg coreconfig.Provider) []string {
	accepted := cfg.K8sServiceAccountConfig().AcceptedKeys()
	out := make([]string, 0, len(accepted))

	for _, k := range accepted {
		out = append(out, string(k.Key))
	}

	return out
}

func pubOf(t *testing.T, bundle *secrets.Bundle) string {
	t.Helper()

	k, err := bundle.Certs.K8sServiceAccount.GetKey()
	if err != nil {
		t.Fatal(err)
	}

	return string(k.GetPublicKeyPEM())
}

func TestKeyRotationStagesRenderTheRightKeys(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewKeyRotation(old, bothKeys)
	if err != nil {
		t.Fatal(err)
	}

	m := testMachine("node-cp", roleControlPlane, nil)
	oldPub, newPub := pubOf(t, old), pubOf(t, next.Apply(old))
	oldBox, newBox := old.Secrets.SecretboxEncryptionSecret, next.Secretbox

	type want struct {
		issuer string
		pubs   []string
		boxes  []string
	}

	oldKey, newKey := string(old.Certs.K8sServiceAccount.Key), string(next.ServiceAccount.Key)

	stages := map[string]want{
		// the old key signs and encrypts; the new one only verifies and decrypts
		ichorv1alpha1.KeyRotationStageAccepting: {oldKey, []string{oldPub, newPub}, []string{oldBox, newBox}},
		// the new key signs and encrypts; the old one is still accepted
		ichorv1alpha1.KeyRotationStageIssuing:        {newKey, []string{newPub, oldPub}, []string{newBox, oldBox}},
		ichorv1alpha1.KeyRotationStageReencrypting:   {newKey, []string{newPub, oldPub}, []string{newBox, oldBox}},
		ichorv1alpha1.KeyRotationStageSettlingTokens: {newKey, []string{newPub, oldPub}, []string{newBox, oldBox}},
		ichorv1alpha1.KeyRotationStageDropping:       {newKey, []string{newPub}, []string{newBox}},
		ichorv1alpha1.KeyRotationStageFinalizing:     {newKey, []string{newPub}, []string{newBox}},
	}

	for stage, w := range stages {
		cfg, err := BuildConfig(cluster, m, old, nil, &SystemInputs{KeyRotation: &KeyRotation{Stage: stage, New: next}})
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}

		if got := string(cfg.K8sServiceAccountConfig().IssuingKey().Key); got != w.issuer {
			t.Errorf("%s: wrong service-account issuing key", stage)
		}

		if got := publicKeys(cfg); !slices.Equal(got, w.pubs) {
			t.Errorf("%s: service-account verification keys differ: got %d, want %d", stage, len(got), len(w.pubs))
		}

		if got := secretboxProviders(t, cfg); !slices.Equal(got, w.boxes) {
			t.Errorf("%s: secretbox providers differ: got %d, want %d (the first encrypts)", stage, len(got), len(w.boxes))
		}
	}
}

// Only the keys named in the request are touched.
func TestKeyRotationRespectsItsTargets(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)
	m := testMachine("node-cp", roleControlPlane, nil)

	box, err := secretsbundle.NewKeyRotation(old, []string{secretsbundle.TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	sa, err := secretsbundle.NewKeyRotation(old, []string{secretsbundle.TargetServiceAccount})
	if err != nil {
		t.Fatal(err)
	}

	issuing := func(kr *secretsbundle.KeyRotation) *SystemInputs {
		return &SystemInputs{KeyRotation: &KeyRotation{Stage: ichorv1alpha1.KeyRotationStageIssuing, New: kr}}
	}

	cfg, err := BuildConfig(cluster, m, old, nil, issuing(box))
	if err != nil {
		t.Fatal(err)
	}

	if got := publicKeys(cfg); !slices.Equal(got, []string{pubOf(t, old)}) {
		t.Error("a secretbox rotation changed the service-account keys")
	}

	cfg, err = BuildConfig(cluster, m, old, nil, issuing(sa))
	if err != nil {
		t.Fatal(err)
	}

	if got := secretboxProviders(t, cfg); !slices.Equal(got, []string{old.Secrets.SecretboxEncryptionSecret}) {
		t.Error("a service-account rotation changed the secretbox providers")
	}
}

// What the nodes run after the last pass is what the replaced bundle renders: the machine controller sees no drift.
func TestKeyRotationLastStagesEqualTheFinalConfiguration(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	for _, targets := range [][]string{bothKeys, {secretsbundle.TargetSecretbox}, {secretsbundle.TargetServiceAccount}} {
		next, err := secretsbundle.NewKeyRotation(old, targets)
		if err != nil {
			t.Fatal(err)
		}

		for _, role := range []string{roleControlPlane, roleWorker} {
			m := testMachine("node-"+role, role, nil)

			_, final, err := Render(cluster, m, next.Apply(old), nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			for _, stage := range []string{ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing} {
				_, got, err := Render(cluster, m, old, nil, &SystemInputs{KeyRotation: &KeyRotation{Stage: stage, New: next}})
				if err != nil {
					t.Fatal(err)
				}

				if got != final {
					t.Errorf("%v %s: the %s configuration differs from the replaced bundle's (drift)", targets, role, stage)
				}
			}

			// Every stage before must differ from the unrotated configuration on a control plane, or nothing would be applied;
			// a worker carries neither document and never changes.
			_, before, _ := Render(cluster, m, old, nil, nil)

			for _, stage := range []string{ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing} {
				_, got, err := Render(cluster, m, old, nil, &SystemInputs{KeyRotation: &KeyRotation{Stage: stage, New: next}})
				if err != nil {
					t.Fatal(err)
				}

				if (role == roleControlPlane) == (got == before) {
					t.Errorf("%v %s %s: control planes must change and workers must not", targets, role, stage)
				}
			}
		}
	}
}

// A provider the bundle does not generate stays where it is, whatever order the rotation inserts around it.
func TestKeyRotationKeepsTheOtherProvidersInPlace(t *testing.T) {
	cluster := testCluster("true")
	old := *testBundle(t)
	secs := *old.Secrets
	secs.AESCBCEncryptionSecret = legacyAESCBC
	old.Secrets = &secs

	next, err := secretsbundle.NewKeyRotation(&old, bothKeys)
	if err != nil {
		t.Fatal(err)
	}

	m := testMachine("node-cp", roleControlPlane, nil)

	for stage, want := range map[string][]string{
		ichorv1alpha1.KeyRotationStageAccepting: {old.Secrets.SecretboxEncryptionSecret, next.Secretbox, legacyAESCBC},
		ichorv1alpha1.KeyRotationStageIssuing:   {next.Secretbox, old.Secrets.SecretboxEncryptionSecret, legacyAESCBC},
		ichorv1alpha1.KeyRotationStageDropping:  {next.Secretbox, legacyAESCBC},
	} {
		cfg, err := BuildConfig(cluster, m, &old, nil, &SystemInputs{KeyRotation: &KeyRotation{Stage: stage, New: next}})
		if err != nil {
			t.Fatal(err)
		}

		if got := secretboxProviders(t, cfg); !slices.Equal(got, want) {
			t.Errorf("%s: providers = %v", stage, got)
		}
	}
}

func TestKeyRotationRefusals(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)
	m := testMachine("node-cp", roleControlPlane, nil)

	next, err := secretsbundle.NewKeyRotation(old, bothKeys)
	if err != nil {
		t.Fatal(err)
	}

	rotation := func(stage string, kr *secretsbundle.KeyRotation) *SystemInputs {
		return &SystemInputs{KeyRotation: &KeyRotation{Stage: stage, New: kr}}
	}

	if _, _, err := Render(cluster, m, old, nil, rotation("Done", next)); err == nil {
		t.Error("a stage no configuration is rendered for must be an error")
	}

	if _, _, err := Render(cluster, m, old, nil, rotation(ichorv1alpha1.KeyRotationStageAccepting, nil)); err == nil {
		t.Error("a rotation without new keys must be an error")
	}

	// A bundle without a secretbox secret has nothing to put the new key next to.
	bare := *old
	secs := *old.Secrets
	secs.SecretboxEncryptionSecret = ""
	bare.Secrets = &secs

	_, _, err = Render(cluster, m, &bare, nil, rotation(ichorv1alpha1.KeyRotationStageAccepting, next))
	if err == nil || !strings.Contains(err.Error(), "secretbox") {
		t.Errorf("a bundle with no secretbox secret must be refused, naming it: %v", err)
	}
}

// The legacy contract keeps the keys in single fields that cannot hold two, so there is nothing to render.
func TestKeyRotationNeedsTheMultiDocumentContract(t *testing.T) {
	cluster := testCluster("true")
	cluster.Spec.VersionContract = contractV113

	contract, err := coreconfig.ParseContractFromVersion(contractV113)
	if err != nil {
		t.Fatal(err)
	}

	clock := secrets.NewFixedClock(testBundle(t).Clock.Now())

	old, err := secrets.NewBundle(clock, contract, secrets.WithECDSAServiceAccountKey())
	if err != nil {
		t.Fatal(err)
	}

	next, err := secretsbundle.NewKeyRotation(old, bothKeys)
	if err != nil {
		t.Fatal(err)
	}

	m := testMachine("node-cp", roleControlPlane, nil)

	_, _, err = Render(cluster, m, old, nil, &SystemInputs{KeyRotation: &KeyRotation{
		Stage: ichorv1alpha1.KeyRotationStageAccepting, New: next,
	}})
	if err == nil || !strings.Contains(err.Error(), "v1.14") {
		t.Fatalf("a v1.13 contract must be refused, naming the contract that works: %v", err)
	}
}
