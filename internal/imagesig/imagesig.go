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

// Package imagesig resolves a container image tag to its digest and verifies the cosign keyless
// signature of that digest (ADR 0122). The signature is an OCI referrer of the image digest, stored as a
// Sigstore bundle (cosign 3's default). The check runs offline against the Sigstore public-good trusted
// root embedded here, so the only network request is the registry's.
package imagesig

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// trustedRootJSON is the Sigstore public-good trusted root (Fulcio, Rekor, CT log and TSA keys),
// fetched through the TUF repository of the sigstore-go release in go.mod. It changes when Sigstore
// rotates its keys; refresh it with the same release.
//
//go:embed trusted_root.json
var trustedRootJSON []byte

const (
	// bundleMediaType is the artifact type of a Sigstore bundle referrer (cosign 3 stores signatures so).
	bundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
	// signPredicate is the predicate type of a cosign signature; other referrers (SBOMs, attestations)
	// are not signatures of the image and are skipped.
	signPredicate = "https://sigstore.dev/cosign/sign/v1"
	predicateKey  = "dev.sigstore.bundle.predicateType"
	// maxBundleBytes bounds the bundle read.
	maxBundleBytes = 64 << 20
	// githubOIDCIssuer is the OIDC issuer of GitHub Actions, which signs the Ichor release images.
	githubOIDCIssuer = "https://token.actions.githubusercontent.com"
	// ichorWorkflow is the workflow that builds, pushes and signs the Ichor images on a release tag
	// (.github/workflows/release.yml).
	ichorWorkflow = "https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/"
)

// Image is one image to check: its repository (a registry path without a scheme) and the digest the
// tag resolved to, "sha256:<hex>".
type Image struct {
	Repository string
	Digest     string
}

// Identity is who must have signed the image: the OIDC issuer of the signing certificate and a regular
// expression the certificate's subject alternative name must match.
type Identity struct {
	Issuer    string
	SANRegexp string
}

// IchorIdentity is the identity of the Ichor release workflow for one release tag. The regular
// expression is anchored and quotes the tag, so only that tag's workflow run matches. It is the identity
// docs/guides/install-with-helm.md gives to `cosign verify`.
func IchorIdentity(tag string) Identity {
	return Identity{
		Issuer:    githubOIDCIssuer,
		SANRegexp: "^" + regexp.QuoteMeta(ichorWorkflow+tag) + "$",
	}
}

// Verifier decides whether an image is signed by the identity. A signature that does not verify is an
// error; the callers never offer or run such an image.
type Verifier interface {
	Verify(ctx context.Context, img Image, id Identity) error
}

// Sigstore verifies signatures with sigstore-go against the embedded trusted root.
type Sigstore struct {
	verifier *verify.Verifier
	// insecure reads an http registry; only tests set it.
	insecure bool
}

// New returns the verifier with the embedded trusted root.
func New() (*Sigstore, error) {
	return newWithRoot(trustedRootJSON, false)
}

func newWithRoot(rootJSON []byte, insecure bool) (*Sigstore, error) {
	tr, err := root.NewTrustedRootFromJSON(rootJSON)
	if err != nil {
		return nil, fmt.Errorf("reading the trusted root: %w", err)
	}

	return newWithMaterial(tr, insecure, 1)
}

// newWithMaterial builds the verifier; scts is how many signed certificate timestamps a certificate must carry
// (the real Fulcio embeds one; a test CA has none).
func newWithMaterial(tr root.TrustedMaterial, insecure bool, scts int) (*Sigstore, error) {
	opts := []verify.VerifierOption{verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1)}
	if scts > 0 {
		opts = append(opts, verify.WithSignedCertificateTimestamps(scts))
	}

	v, err := verify.NewVerifier(tr, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating the verifier: %w", err)
	}

	return &Sigstore{verifier: v, insecure: insecure}, nil
}

// Verify succeeds when one cosign signature of the image digest verifies for the identity. It reads the
// signature referrers of the digest, checks each bundle, and returns the first success.
func (s *Sigstore) Verify(ctx context.Context, img Image, id Identity) error {
	digest, err := sha256Bytes(img.Digest)
	if err != nil {
		return err
	}

	repo, err := s.repository(img.Repository)
	if err != nil {
		return err
	}

	ref := repo.Digest(img.Digest)

	idx, err := remote.Referrers(ref, remote.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("listing the signatures of %s@%s: %w", img.Repository, img.Digest, err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		return fmt.Errorf("reading the signatures of %s@%s: %w", img.Repository, img.Digest, err)
	}

	var errs []error

	for _, d := range im.Manifests {
		if d.ArtifactType != bundleMediaType || d.Annotations[predicateKey] != signPredicate {
			continue
		}

		b, err := s.bundle(ctx, repo, d.Digest)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		if err := s.check(b, digest, id); err != nil {
			errs = append(errs, fmt.Errorf("signature %s: %w", d.Digest, err))

			continue
		}

		return nil
	}

	if len(errs) == 0 {
		return fmt.Errorf("%s@%s has no cosign signature", img.Repository, img.Digest)
	}

	return fmt.Errorf("%s@%s is not signed by %s: %w", img.Repository, img.Digest, id.SANRegexp, errors.Join(errs...))
}

// check verifies one signed entity against the digest and the identity.
func (s *Sigstore) check(entity verify.SignedEntity, digest []byte, id Identity) error {
	certID, err := verify.NewShortCertificateIdentity(id.Issuer, "", "", id.SANRegexp)
	if err != nil {
		return fmt.Errorf("the signing identity: %w", err)
	}

	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest), verify.WithCertificateIdentity(certID))

	_, err = s.verifier.Verify(entity, policy)

	return err
}

// ResolveDigest is the digest the tag of a repository points at now, "sha256:<hex>", from a HEAD request
// on the manifest. A mirror's tag is resolved at the mirror.
func ResolveDigest(ctx context.Context, repository, tag string) (string, error) {
	return resolveDigest(ctx, repository, tag, false)
}

func resolveDigest(ctx context.Context, repository, tag string, insecure bool) (string, error) {
	var opts []name.Option
	if insecure {
		opts = append(opts, name.Insecure)
	}

	ref, err := name.NewTag(repository+":"+tag, opts...)
	if err != nil {
		return "", fmt.Errorf("the image %s:%s: %w", repository, tag, err)
	}

	desc, err := remote.Head(ref, remote.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("resolving %s:%s to a digest: %w", repository, tag, err)
	}

	d := desc.Digest.String()
	if _, err := sha256Bytes(d); err != nil {
		return "", err
	}

	return d, nil
}

// repository parses the repository, with plain http when the verifier was built for tests.
func (s *Sigstore) repository(repo string) (name.Repository, error) {
	var opts []name.Option
	if s.insecure {
		opts = append(opts, name.Insecure)
	}

	r, err := name.NewRepository(repo, opts...)
	if err != nil {
		return name.Repository{}, fmt.Errorf("the repository %q: %w", repo, err)
	}

	return r, nil
}

// bundle reads the Sigstore bundle of a signature referrer: the layer of its manifest with the bundle
// media type.
func (s *Sigstore) bundle(ctx context.Context, repo name.Repository, digest v1.Hash) (*bundle.Bundle, error) {
	desc, err := remote.Get(repo.Digest(digest.String()), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("reading the signature manifest %s: %w", digest, err)
	}

	var m v1.Manifest
	if err := json.Unmarshal(desc.Manifest, &m); err != nil {
		return nil, fmt.Errorf("parsing the signature manifest %s: %w", digest, err)
	}

	for _, l := range m.Layers {
		if l.MediaType != bundleMediaType {
			continue
		}

		layer, err := remote.Layer(repo.Digest(l.Digest.String()), remote.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("reading the bundle %s: %w", l.Digest, err)
		}

		rc, err := layer.Compressed()
		if err != nil {
			return nil, fmt.Errorf("reading the bundle %s: %w", l.Digest, err)
		}

		data, err := io.ReadAll(io.LimitReader(rc, maxBundleBytes+1))
		_ = rc.Close()

		if err != nil {
			return nil, fmt.Errorf("reading the bundle %s: %w", l.Digest, err)
		}

		if len(data) > maxBundleBytes {
			return nil, fmt.Errorf("the bundle %s is larger than %d bytes", l.Digest, maxBundleBytes)
		}

		b := &bundle.Bundle{}
		if err := b.UnmarshalJSON(data); err != nil {
			return nil, fmt.Errorf("parsing the bundle %s: %w", l.Digest, err)
		}

		return b, nil
	}

	return nil, fmt.Errorf("the signature manifest %s has no bundle layer", digest)
}

// sha256Bytes is the digest bytes of "sha256:<hex>"; any other algorithm is refused.
func sha256Bytes(digest string) ([]byte, error) {
	hexPart, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return nil, fmt.Errorf("the digest %q is not sha256", digest)
	}

	b, err := hex.DecodeString(hexPart)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("the digest %q is not a sha256 digest", digest)
	}

	return b, nil
}
