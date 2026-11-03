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
	"os"
	"regexp"
	"strings"
	"testing"
)

// certificateSAN is the subject alternative name of the agent image's signing certificate for
// quay.io/cilium/cilium:v1.20.4, read from its bundle (Fulcio certificate, 2026-10-23).
const certificateSAN = "https://github.com/cilium/cilium/.github/workflows/build-images-releases.yaml@refs/tags/v1.20.4"

// ciliumAgentDigest is the digest of quay.io/cilium/cilium:v1.20.4 (the embedded chart's agent image).
const ciliumAgentDigest = "sha256:20fe92e4c86641233a450098c158d2d29085f3419931ab6bf681d2485dedf16e"

func TestCiliumIdentityMatchesOnlyItsTag(t *testing.T) {
	id := CiliumIdentity("v1.20.4")

	if id.Issuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("issuer = %q", id.Issuer)
	}

	re := regexp.MustCompile(id.SANRegexp)
	if !re.MatchString(certificateSAN) {
		t.Fatalf("the identity %q does not match the signing certificate's SAN %q", id.SANRegexp, certificateSAN)
	}

	for _, other := range []string{
		"https://github.com/cilium/cilium/.github/workflows/build-images-releases.yaml@refs/tags/v1.20.45",
		"https://github.com/cilium/cilium/.github/workflows/build-images-releases.yaml@refs/tags/v1.20.4x",
		"https://github.com/evil/cilium/.github/workflows/build-images-releases.yaml@refs/tags/v1.20.4",
	} {
		if re.MatchString(other) {
			t.Errorf("the identity matches %q", other)
		}
	}
}

func TestSha256BytesRefusesOtherDigests(t *testing.T) {
	if _, err := sha256Bytes(ciliumAgentDigest); err != nil {
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

// fakeRegistry serves the referrers of one image digest. Its signature referrers are given by the
// test; the blobs are served by digest.
type fakeRegistry struct {
	subject   string
	referrers []map[string]any
	blobs     map[string][]byte
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/v2/cilium/cilium/referrers/"+f.subject:
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.index.v1+json",
			"manifests":     f.referrers,
		})
	case strings.HasPrefix(r.URL.Path, "/v2/cilium/cilium/manifests/") || strings.HasPrefix(r.URL.Path, "/v2/cilium/cilium/blobs/"):
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

func newFakeRegistry(t *testing.T) (*httptest.Server, *fakeRegistry) {
	t.Helper()

	f := &fakeRegistry{subject: ciliumAgentDigest, blobs: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	return srv, f
}

func TestVerifyRefusesAnImageWithoutASignature(t *testing.T) {
	srv, _ := newFakeRegistry(t)

	s, err := newWithRoot(trustedRootJSON, true)
	if err != nil {
		t.Fatal(err)
	}

	img := Image{Repository: strings.TrimPrefix(srv.URL, "http://") + "/cilium/cilium", Digest: ciliumAgentDigest}

	err = s.Verify(context.Background(), img, CiliumIdentity("v1.20.4"))
	if err == nil || !strings.Contains(err.Error(), "has no cosign signature") {
		t.Fatalf("an unsigned image: err = %v", err)
	}
}

func TestVerifyRefusesASignatureThatDoesNotVerify(t *testing.T) {
	srv, f := newFakeRegistry(t)
	f.referrers = []map[string]any{signatureReferrer(t, f, []byte(`{"mediaType":"not a bundle"}`))}

	s, err := newWithRoot(trustedRootJSON, true)
	if err != nil {
		t.Fatal(err)
	}

	img := Image{Repository: strings.TrimPrefix(srv.URL, "http://") + "/cilium/cilium", Digest: ciliumAgentDigest}

	err = s.Verify(context.Background(), img, CiliumIdentity("v1.20.4"))
	if err == nil || !strings.Contains(err.Error(), "is not signed by") {
		t.Fatalf("a signature that does not parse: err = %v", err)
	}
}

// TestVerifyCiliumAgentLive checks the real signature of quay.io/cilium/cilium:v1.20.4 against the
// embedded trusted root. It reaches quay.io, so it runs only when ICHOR_LIVE_IMAGE_SIGNATURES is set.
func TestVerifyCiliumAgentLive(t *testing.T) {
	if os.Getenv("ICHOR_LIVE_IMAGE_SIGNATURES") == "" {
		t.Skip("set ICHOR_LIVE_IMAGE_SIGNATURES=1 to verify the Cilium image signature against quay.io")
	}

	s, err := New()
	if err != nil {
		t.Fatal(err)
	}

	// The digests are the chart's pins for v1.20.4: the agent, the operator variant the chart renders
	// on a cloud-less cluster (operator-generic), and hubble-relay.
	images := []Image{
		{Repository: "quay.io/cilium/cilium", Digest: ciliumAgentDigest},
		{Repository: "quay.io/cilium/operator-generic", Digest: "sha256:7a7a3489dc66ad67e420d285c8a8d2e169b0957ad15e120f593d27081bf38d82"},
		{Repository: "quay.io/cilium/hubble-relay", Digest: "sha256:cb23408209697ed30d2bc462e1f01eb622ea4423a891458f78bc88d7255bbfc1"},
	}

	for _, img := range images {
		if err := s.Verify(context.Background(), img, CiliumIdentity("v1.20.4")); err != nil {
			t.Errorf("the signature of %s@%s does not verify: %v", img.Repository, img.Digest, err)
		}
	}
}
