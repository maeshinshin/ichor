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

package secretsbundle

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const (
	secondSecret = "second"
	oldSecret    = "old"
)

func TestKeyRotationRoundTripAndApply(t *testing.T) {
	bundle := newTestBundle(t)

	kr, err := NewKeyRotation(bundle, []string{TargetServiceAccount, TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := kr.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	back, err := ParseKeyRotation(raw)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(back.ServiceAccount.Key, kr.ServiceAccount.Key) || back.Secretbox != kr.Secretbox {
		t.Fatal("the new keys did not survive the round trip")
	}

	applied := kr.Apply(bundle)
	if !bytes.Equal(applied.Certs.K8sServiceAccount.Key, kr.ServiceAccount.Key) || applied.Secrets.SecretboxEncryptionSecret != kr.Secretbox {
		t.Fatal("Apply did not replace the keys")
	}

	if applied.Cluster != bundle.Cluster || applied.Certs.K8s != bundle.Certs.K8s {
		t.Fatal("Apply changed something other than the service-account key and the secretbox secret")
	}

	if bytes.Equal(bundle.Certs.K8sServiceAccount.Key, kr.ServiceAccount.Key) || bundle.Secrets.SecretboxEncryptionSecret == kr.Secretbox {
		t.Fatal("Apply modified its input")
	}
}

func TestNewKeyRotationRespectsTargetsAndKeyType(t *testing.T) {
	bundle := newTestBundle(t)

	only, err := NewKeyRotation(bundle, []string{TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	if only.ServiceAccount != nil || only.Secretbox == "" {
		t.Fatalf("a secretbox-only rotation must not generate a service-account key: %+v", only)
	}

	applied := only.Apply(bundle)
	if applied.Certs.K8sServiceAccount != bundle.Certs.K8sServiceAccount {
		t.Fatal("Apply replaced the service-account key of a secretbox-only rotation")
	}

	secret, err := base64.StdEncoding.DecodeString(only.Secretbox)
	if err != nil || len(secret) != 32 {
		t.Fatalf("the secretbox secret must be 32 bytes of standard base64: %d %v", len(secret), err)
	}

	// The new service-account key has the algorithm of the old one: the issuer's consumers accept one.
	old, err := bundle.Certs.K8sServiceAccount.GetKey()
	if err != nil {
		t.Fatal(err)
	}

	both, err := NewKeyRotation(bundle, []string{TargetServiceAccount})
	if err != nil {
		t.Fatal(err)
	}

	fresh, err := both.ServiceAccount.GetKey()
	if err != nil {
		t.Fatal(err)
	}

	if old.GetPublicKeyPEM() == nil || strings.EqualFold(string(old.GetPublicKeyPEM()), string(fresh.GetPublicKeyPEM())) {
		t.Fatal("the new service-account key must differ from the old one")
	}

	if _, err := NewKeyRotation(bundle, nil); err == nil {
		t.Fatal("a rotation of no key must be refused")
	}

	if _, err := NewKeyRotation(bundle, []string{"aescbc"}); err == nil {
		t.Fatal("an unknown target must be refused")
	}
}

func TestParseKeyRotationErrorsCarryNoContent(t *testing.T) {
	_, err := ParseKeyRotation([]byte("secretbox: c2VjcmV0LWtleQ==\n"))
	if err == nil {
		t.Fatal("a secretbox secret of the wrong length must be refused")
	}

	if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "c2VjcmV0") {
		t.Fatalf("error carries key material: %v", err)
	}

	_, err = ParseKeyRotation([]byte("serviceAccount: not-a-key\n"))
	if err == nil {
		t.Fatal("a service-account key that is not a PEM key must be refused")
	}

	if _, err := ParseKeyRotation([]byte("{}\n")); err == nil {
		t.Fatal("a file with no key must be refused")
	}
}

func TestLoadKeyRotationAbsentAndPresent(t *testing.T) {
	secret := &corev1.Secret{Namespace: testNamespace, Name: testName, Data: map[string][]byte{}}

	got, err := LoadKeyRotation(context.Background(), newFakeClient(secret), testNamespace, testName)
	if err != nil || got != nil {
		t.Fatalf("no keyrotation.yaml means no key rotation, got %v %v", got, err)
	}

	kr, err := NewKeyRotation(newTestBundle(t), []string{TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := kr.Marshal()
	secret.Data[KeyRotationKey] = raw

	got, err = LoadKeyRotation(context.Background(), newFakeClient(secret), testNamespace, testName)
	if err != nil || got == nil || got.Secretbox != kr.Secretbox {
		t.Fatalf("keyrotation.yaml was not read back: %v %v", got, err)
	}
}

func TestKeyIdentifiersArePublicAndStable(t *testing.T) {
	bundle := newTestBundle(t)

	a, err := ServiceAccountFingerprint(bundle.Certs.K8sServiceAccount)
	if err != nil {
		t.Fatal(err)
	}

	b, err := ServiceAccountFingerprint(bundle.Certs.K8sServiceAccount)
	if err != nil || a != b || len(a) != 64 {
		t.Fatalf("the service-account fingerprint must be a stable sha256: %q %q %v", a, b, err)
	}

	id := SecretboxID(bundle.Secrets.SecretboxEncryptionSecret)
	if id == "" || strings.Contains(bundle.Secrets.SecretboxEncryptionSecret, id) || id != SecretboxID(bundle.Secrets.SecretboxEncryptionSecret) {
		t.Fatalf("the secretbox id must be stable and not a part of the secret: %q", id)
	}

	if SecretboxID("other") == id {
		t.Fatal("different secrets must have different ids")
	}
}

func TestRetiredKeysRoundTripOrderAndFingerprint(t *testing.T) {
	var r Retired

	if got := r.Secretboxes(); len(got) != 0 {
		t.Fatalf("an empty list retires nothing: %v", got)
	}

	r.Add(RetiredKey{RetiredAt: time.Unix(1000, 0).UTC(), Secretbox: "first"})
	r.Add(RetiredKey{RetiredAt: time.Unix(2000, 0).UTC(), ServiceAccount: "fp", Secretbox: secondSecret})
	r.Add(RetiredKey{RetiredAt: time.Unix(3000, 0).UTC(), ServiceAccount: "only-sa"})

	if got := r.Secretboxes(); len(got) != 2 || got[0] != secondSecret || got[1] != "first" {
		t.Fatalf("secretbox secrets must be newest first and skip entries without one: %v", got)
	}

	raw, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	back, err := ParseRetired(raw)
	if err != nil {
		t.Fatal(err)
	}

	if len(back.Keys) != 3 || back.Keys[1].Secretbox != secondSecret || !back.Keys[2].RetiredAt.Equal(time.Unix(3000, 0)) {
		t.Fatalf("retired keys did not survive the round trip: %+v", back)
	}

	if back.Fingerprint() != r.Fingerprint() || r.Fingerprint() == "" {
		t.Fatal("the fingerprint must be stable and non-empty for a non-empty list")
	}

	var empty *Retired
	if empty.Fingerprint() != "" || len(empty.Secretboxes()) != 0 {
		t.Fatal("a nil list must behave as an empty one")
	}

	more := *back
	more.Add(RetiredKey{RetiredAt: time.Unix(4000, 0).UTC(), Secretbox: "third"})

	if more.Fingerprint() == back.Fingerprint() {
		t.Fatal("a further rotation must change the fingerprint")
	}
}

func TestParseRetiredErrorsCarryNoContent(t *testing.T) {
	_, err := ParseRetired([]byte("retired:\n  - secretbox: [c2VjcmV0LWtleQ==\n"))
	if err == nil {
		t.Fatal("malformed YAML must be refused")
	}

	if strings.Contains(err.Error(), "c2VjcmV0") {
		t.Fatalf("error carries key material: %v", err)
	}
}

func TestLoadRetiredAbsentAndPresent(t *testing.T) {
	secret := &corev1.Secret{Namespace: testNamespace, Name: testName, Data: map[string][]byte{}}

	got, err := LoadRetired(context.Background(), newFakeClient(secret), testNamespace, testName)
	if err != nil || got == nil || len(got.Keys) != 0 {
		t.Fatalf("no retired-keys.yaml means an empty list, got %v %v", got, err)
	}

	var r Retired

	r.Add(RetiredKey{RetiredAt: time.Unix(1, 0).UTC(), Secretbox: oldSecret})
	secret.Data[RetiredKeysKey], _ = r.Marshal()

	got, err = LoadRetired(context.Background(), newFakeClient(secret), testNamespace, testName)
	if err != nil || len(got.Keys) != 1 || got.Keys[0].Secretbox != oldSecret {
		t.Fatalf("retired-keys.yaml was not read back: %v %v", got, err)
	}
}

func TestExportFingerprintUnchangedWithoutRetiredKeys(t *testing.T) {
	bundle := newTestBundle(t)

	base, err := ExportFingerprint(bundle, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range []*Retired{nil, {}} {
		got, err := ExportFingerprint(bundle, r)
		if err != nil || got != base {
			t.Fatalf("a cluster that never rotated keys keeps its export fingerprint: %q %q %v", got, base, err)
		}
	}

	var r Retired

	r.Add(RetiredKey{RetiredAt: time.Unix(1, 0).UTC(), Secretbox: oldSecret})

	got, err := ExportFingerprint(bundle, &r)
	if err != nil || got == base || got == "" {
		t.Fatalf("a retired key must change the export fingerprint: %q %q %v", got, base, err)
	}
}
