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
	"context"
	stdx509 "crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNamespace = "default"
	testName      = "bundle"
)

func newTestBundle(t *testing.T) *secrets.Bundle {
	t.Helper()

	contract, err := coreconfig.ParseContractFromVersion("v1.14")
	if err != nil {
		t.Fatalf("ParseContractFromVersion: %v", err)
	}

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	return bundle
}

// newFakeClient builds a client over the default client-go scheme, which knows Secrets.
func newFakeClient(objs ...*corev1.Secret) client.Client {
	b := fake.NewClientBuilder()
	for _, o := range objs {
		b = b.WithObjects(o)
	}

	return b.Build()
}

// bundleSecret is the Secret that holds raw under key.
func bundleSecret(key string, raw []byte) *corev1.Secret {
	return &corev1.Secret{Namespace: testNamespace, Name: testName, Data: map[string][]byte{key: raw}}
}

func marshalBundle(t *testing.T, bundle *secrets.Bundle) []byte {
	t.Helper()

	raw, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	return raw
}

// bundleSecretMaterial lists every sensitive scalar in bundle, the same
// way internal/controller's and pkg/render's own leak tests do for
// theirs.
func bundleSecretMaterial(bundle *secrets.Bundle) []string {
	return []string{
		bundle.Secrets.BootstrapToken,
		bundle.TrustdInfo.Token,
		bundle.Cluster.ID,
		bundle.Cluster.Secret,
		string(bundle.Certs.OS.Crt),
		string(bundle.Certs.OS.Key),
		string(bundle.Certs.K8s.Crt),
		string(bundle.Certs.K8s.Key),
	}
}

func assertNoLeak(t *testing.T, err error, secretMaterial []string) {
	t.Helper()

	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	for _, s := range secretMaterial {
		if s == "" {
			continue
		}

		if strings.Contains(err.Error(), s) {
			t.Fatalf("error message leaked bundle material: %v", err)
		}
	}
}

func TestLoadRoundTrip(t *testing.T) {
	bundle := newTestBundle(t)

	secret := bundleSecret(SecretKey, marshalBundle(t, bundle))

	got, err := Load(context.Background(), newFakeClient(secret), testNamespace, testName)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Cluster.ID != bundle.Cluster.ID {
		t.Fatalf("Load round-tripped a different bundle: got Cluster.ID %q, want %q", got.Cluster.ID, bundle.Cluster.ID)
	}
}

// TestLoadFillsClock guards ADR 0032's finding: Bundle.Clock carries
// yaml:"-" and nothing refills it on decode except the machinery
// package's own populate(), which only runs while building a brand-new
// bundle. Confirmed for real that leaving it nil panics the first time
// GenerateTalosAPIClientCertificate is called on a bundle loaded this
// way, with a nil-interface Clock.Now().
func TestLoadFillsClock(t *testing.T) {
	bundle := newTestBundle(t)

	secret := bundleSecret(SecretKey, marshalBundle(t, bundle))

	got, err := Load(context.Background(), newFakeClient(secret), testNamespace, testName)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Clock == nil {
		t.Fatal("Load left Clock nil; the first GenerateTalosAPIClientCertificate call would panic")
	}

	if _, err := got.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin)); err != nil {
		t.Fatalf("GenerateTalosAPIClientCertificate: %v", err)
	}
}

func TestLoadSecretNotFound(t *testing.T) {
	_, err := Load(context.Background(), newFakeClient(), testNamespace, testName)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestLoadMissingKeyDoesNotLeak(t *testing.T) {
	bundle := newTestBundle(t)

	_, err := Load(context.Background(), newFakeClient(bundleSecret("wrong-key", []byte("irrelevant"))), testNamespace, testName)
	assertNoLeak(t, err, bundleSecretMaterial(bundle))
}

// TestLoadMalformedYAMLDoesNotLeak is the one path that actually matters:
// raw holds the real, encoded bundle bytes right up until Load fails to
// decode them, so this is the only test here that can prove the decode
// failure's error message does not quote any of those bytes back.
func TestLoadMalformedYAMLDoesNotLeak(t *testing.T) {
	bundle := newTestBundle(t)

	// A leading tab is never valid YAML indentation, so this is
	// guaranteed to fail decoding rather than parse as something else.
	raw := append([]byte("\tnot valid yaml\n"), marshalBundle(t, bundle)...)

	_, err := Load(context.Background(), newFakeClient(bundleSecret(SecretKey, raw)), testNamespace, testName)
	assertNoLeak(t, err, bundleSecretMaterial(bundle))
}

// TestDecodedBundleClientCertificateIsBackdated: a node whose clock is a little behind the
// controller's must still accept the os:admin certificate minted for a dial.
func TestDecodedBundleClientCertificateIsBackdated(t *testing.T) {
	got, err := Decode(marshalBundle(t, newTestBundle(t)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	pair, err := got.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		t.Fatalf("GenerateTalosAPIClientCertificate: %v", err)
	}

	block, _ := pem.Decode(pair.Crt)
	if block == nil {
		t.Fatal("no certificate PEM")
	}

	cert, err := stdx509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}

	if want := time.Now().Add(-clockSkew / 2); cert.NotBefore.After(want) {
		t.Fatalf("NotBefore %s is not backdated (want before %s)", cert.NotBefore, want)
	}
}
