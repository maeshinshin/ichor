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

package imagesig

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
)

// certificateSAN is the subject alternative name Fulcio puts in the certificate of a signature made by
// release.yml on the tag v0.1.0.
const certificateSAN = "https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/v0.1.0"

const testDigest = "sha256:20fe92e4c86641233a450098c158d2d29085f3419931ab6bf681d2485dedf16e"

func TestIchorIdentityMatchesOnlyItsTag(t *testing.T) {
	id := IchorIdentity("v0.1.0")

	if id.Issuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("issuer = %q", id.Issuer)
	}

	re := regexp.MustCompile(id.SANRegexp)
	if !re.MatchString(certificateSAN) {
		t.Fatalf("the identity %q does not match the signing certificate's SAN %q", id.SANRegexp, certificateSAN)
	}

	for _, other := range []string{
		"https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/v0.1.01",
		"https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/v0.1.0-rc1",
		"https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/heads/main",
		"https://github.com/maeshinshin/ichor/.github/workflows/other.yml@refs/tags/v0.1.0",
		"https://github.com/evil/ichor/.github/workflows/release.yml@refs/tags/v0.1.0",
		"https://github.com/maeshinshin/ichorXgithub/workflows/release.yml@refs/tags/v0.1.0",
	} {
		if re.MatchString(other) {
			t.Errorf("the identity matches %q", other)
		}
	}
}

func TestSha256BytesRefusesOtherDigests(t *testing.T) {
	if _, err := sha256Bytes(testDigest); err != nil {
		t.Fatalf("a sha256 digest: %v", err)
	}

	for _, bad := range []string{"sha512:" + strings.Repeat("a", 128), "sha256:abc", "sha256:zz" + strings.Repeat("a", 62), ""} {
		if _, err := sha256Bytes(bad); err == nil {
			t.Errorf("sha256Bytes(%q) accepted a digest that is not sha256", bad)
		}
	}
}

func TestEmbeddedTrustedRootLoads(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("the embedded trusted root: %v", err)
	}
}

// TestCheckAcceptsOnlyTheIdentityAndTheDigest signs an artifact in a virtual Sigstore (a test Fulcio,
// Rekor and timestamp authority) and runs the policy Verify applies to every bundle it finds.
func TestCheckAcceptsOnlyTheIdentityAndTheDigest(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}

	s, err := newWithMaterial(vs, false, 0)
	if err != nil {
		t.Fatal(err)
	}

	artifact := []byte("an image manifest")
	sum := sha256.Sum256(artifact)
	other := sha256.Sum256([]byte("another manifest"))

	entity, err := vs.Sign(certificateSAN, githubOIDCIssuer, artifact)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.check(entity, sum[:], IchorIdentity("v0.1.0")); err != nil {
		t.Fatalf("the right identity and digest: %v", err)
	}

	if err := s.check(entity, other[:], IchorIdentity("v0.1.0")); err == nil {
		t.Error("a signature of another digest verified")
	}

	if err := s.check(entity, sum[:], IchorIdentity("v0.2.0")); err == nil {
		t.Error("a signature of another release tag verified")
	}

	wrongIssuer := IchorIdentity("v0.1.0")
	wrongIssuer.Issuer = "https://accounts.example.com"

	if err := s.check(entity, sum[:], wrongIssuer); err == nil {
		t.Error("a signature from another issuer verified")
	}
}

// TestCheckRefusesACertificateFromAnotherCA is the unsigned-by-Sigstore case: a signature whose
// certificate chains to a CA the trusted root does not hold.
func TestCheckRefusesACertificateFromAnotherCA(t *testing.T) {
	signer, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}

	trusted, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}

	s, err := newWithMaterial(trusted, false, 0)
	if err != nil {
		t.Fatal(err)
	}

	artifact := []byte("an image manifest")
	sum := sha256.Sum256(artifact)

	entity, err := signer.Sign(certificateSAN, githubOIDCIssuer, artifact)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.check(entity, sum[:], IchorIdentity("v0.1.0")); err == nil {
		t.Error("a signature from an untrusted CA verified")
	}
}

// fakeRegistry serves the referrers of one image digest. Its signature referrers are given by the
// test; the blobs are served by digest.
type fakeRegistry struct {
	subject   string
	referrers []map[string]any
	blobs     map[string][]byte
}

const fakeRepo = "maeshinshin/ichor-installer"

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/v2/"+fakeRepo+"/referrers/"+f.subject:
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.index.v1+json",
			"manifests":     f.referrers,
		})
	case strings.HasPrefix(r.URL.Path, "/v2/"+fakeRepo+"/manifests/") || strings.HasPrefix(r.URL.Path, "/v2/"+fakeRepo+"/blobs/"):
		digest := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		data, ok := f.blobs[digest]
		if !ok {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// digestOf is the sha256 digest of data in the registry's form.
func digestOf(data []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

// signatureReferrer serves a signature whose bundle is bundleBody, and returns the referrer entry of
// the index.
func signatureReferrer(t *testing.T, f *fakeRegistry, bundleBody []byte) map[string]any {
	t.Helper()

	layer := digestOf(bundleBody)
	f.blobs[layer] = bundleBody

	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.oci.empty.v1+json", "size": 2, "digest": digestOf([]byte("{}"))},
		"layers": []any{map[string]any{
			"mediaType": bundleMediaType, "size": len(bundleBody), "digest": layer,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	md := digestOf(manifest)
	f.blobs[md] = manifest

	return map[string]any{
		"mediaType":    "application/vnd.oci.image.manifest.v1+json",
		"digest":       md,
		"size":         len(manifest),
		"artifactType": bundleMediaType,
		"annotations":  map[string]any{predicateKey: signPredicate},
	}
}

// verifyAgainst runs Verify on the fake registry's installer image with the embedded trusted root.
func verifyAgainst(t *testing.T, f *fakeRegistry, srv *httptest.Server) error {
	t.Helper()

	s, err := newWithRoot(trustedRootJSON, true)
	if err != nil {
		t.Fatal(err)
	}

	img := Image{Repository: strings.TrimPrefix(srv.URL, "http://") + "/" + fakeRepo, Digest: f.subject}

	return s.Verify(context.Background(), img, IchorIdentity("v0.1.0"))
}

func newFakeRegistry(t *testing.T) (*httptest.Server, *fakeRegistry) {
	t.Helper()

	f := &fakeRegistry{subject: testDigest, blobs: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	return srv, f
}

func TestVerifyRefusesAnImageWithoutASignature(t *testing.T) {
	srv, f := newFakeRegistry(t)

	err := verifyAgainst(t, f, srv)
	if err == nil || !strings.Contains(err.Error(), "has no cosign signature") {
		t.Fatalf("an unsigned image: err = %v", err)
	}
}

func TestVerifyRefusesASignatureThatDoesNotVerify(t *testing.T) {
	srv, f := newFakeRegistry(t)
	f.referrers = []map[string]any{signatureReferrer(t, f, []byte(`{"mediaType":"not a bundle"}`))}

	err := verifyAgainst(t, f, srv)
	if err == nil || !strings.Contains(err.Error(), "is not signed by") {
		t.Fatalf("a signature that does not parse: err = %v", err)
	}
}

func TestVerifyIgnoresReferrersThatAreNotCosignSignatures(t *testing.T) {
	srv, f := newFakeRegistry(t)

	ref := signatureReferrer(t, f, []byte(`{}`))
	ref["annotations"] = map[string]any{predicateKey: "https://slsa.dev/provenance/v1"}
	f.referrers = []map[string]any{ref}

	err := verifyAgainst(t, f, srv)
	if err == nil || !strings.Contains(err.Error(), "has no cosign signature") {
		t.Fatalf("an attestation is not a signature: err = %v", err)
	}
}

func TestResolveDigest(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")

	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := name.NewTag(host+"/maeshinshin/ichor-installer:v0.1.0", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}

	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}

	want, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}

	got, err := resolveDigest(context.Background(), host+"/maeshinshin/ichor-installer", "v0.1.0", true)
	if err != nil {
		t.Fatal(err)
	}

	if got != want.String() {
		t.Errorf("digest = %s, want %s", got, want)
	}

	if _, err := resolveDigest(context.Background(), host+"/maeshinshin/ichor-installer", "v9.9.9", true); err == nil {
		t.Error("a tag that does not exist resolved")
	}
}
