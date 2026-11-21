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
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The fixture is the real keyless signature of the v0.2.4 release: testdata/ichor_0.2.4_checksums.txt and its
// bundle ichor_0.2.4_checksums.txt.sigstore.json are the two release assets of that name, downloaded from
// https://github.com/maeshinshin/ichor/releases/tag/v0.2.4 unchanged. The bundle is signed by release.yml on
// the tag v0.2.4 and carries its certificate and transparency-log entry, so it verifies offline, long after
// the certificate expired.

// TestVerifyBlobAcceptsARealReleaseBundle: a valid bundle passes; a digest of other bytes and an identity of
// another tag are refused.
func TestVerifyBlobAcceptsARealReleaseBundle(t *testing.T) {
	blob, err := os.ReadFile("testdata/ichor_0.2.4_checksums.txt")
	if err != nil {
		t.Fatal(err)
	}

	bundleJSON, err := os.ReadFile("testdata/ichor_0.2.4_checksums.txt.sigstore.json")
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(blob)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	s, err := New()
	if err != nil {
		t.Fatal(err)
	}

	if err := s.VerifyBlob(bundleJSON, digest, IchorIdentity("v0.2.4")); err != nil {
		t.Fatalf("the real v0.2.4 bundle: err = %v", err)
	}

	sum[0] ^= 1
	if err := s.VerifyBlob(bundleJSON, "sha256:"+hex.EncodeToString(sum[:]), IchorIdentity("v0.2.4")); err == nil || !strings.Contains(err.Error(), "is not signed by") {
		t.Fatalf("a flipped digest: err = %v", err)
	}

	if err := s.VerifyBlob(bundleJSON, digest, IchorIdentity("v0.2.3")); err == nil || !strings.Contains(err.Error(), "is not signed by") {
		t.Fatalf("the identity of another tag: err = %v", err)
	}
}
