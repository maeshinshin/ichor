package verifydocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// attested maps what release.yml uses to attest to the predicate type it produces. A
// `cosign attest --type` in the workflow is taken as written.
var attested = map[string]string{
	"actions/attest-build-provenance": "https://slsa.dev/provenance/v1",
	"actions/attest-sbom":             "https://spdx.dev/Document/v2.3", // SPDX 2.3 JSON, as the SBOMs are
}

var (
	cosignAttest = regexp.MustCompile(`cosign attest\b[^\n]*--type[ =](\S+)`)
	verifyType   = regexp.MustCompile(`cosign verify-attestation\b[^\n]*?--type[ =](\S+)`)
	// cosign's own short name for the SLSA v1 predicate
	aliases = map[string]string{"slsaprovenance1": "https://slsa.dev/provenance/v1"}
)

func allowed(t *testing.T) map[string]bool {
	b, err := os.ReadFile("../../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	ok := map[string]bool{}
	for action, typ := range attested {
		if strings.Contains(string(b), action+"@") {
			ok[typ] = true
		}
	}
	for _, m := range cosignAttest.FindAllStringSubmatch(string(b), -1) {
		ok[m[1]] = true
	}
	if len(ok) == 0 {
		t.Fatal("release.yml attests nothing")
	}
	return ok
}

// TestVerifyAttestationTypes holds every `cosign verify-attestation` in the docs to a predicate type the
// release workflow attests; a type it never attests fails with "none of the attestations matched".
func TestVerifyAttestationTypes(t *testing.T) {
	ok := allowed(t)
	roots := []string{"../../../docs", "../../../README.md"}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || strings.Contains(p, "/docs/adr/") {
				return err
			}
			b, _ := os.ReadFile(p)
			md := strings.ReplaceAll(string(b), "\\\n", " ")
			for _, m := range verifyType.FindAllStringSubmatch(md, -1) {
				typ := m[1]
				if a, found := aliases[typ]; found {
					typ = a
				}
				if !ok[typ] {
					t.Errorf("%s: cosign verify-attestation --type %s is not attested by release.yml (want one of %v)", p, m[1], ok)
				}
			}
			return nil
		})
	}
}
