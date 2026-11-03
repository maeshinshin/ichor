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

// Package imagesig verifies the cosign keyless signatures of the container images an upgrade would
// run. Cilium signs its images in its release workflow since 1.13; the signature is an OCI
// referrer of the image digest, stored as a Sigstore bundle. The check runs offline against the
// Sigstore public-good trusted root embedded here, so the only network request is the registry's.
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
	// maxBundleBytes bounds the bundle read; Cilium's agent signature is about 8.5 MB.
	maxBundleBytes = 64 << 20
	// githubOIDCIssuer is the OIDC issuer of GitHub Actions, which signs the Cilium release images.
	githubOIDCIssuer = "https://token.actions.githubusercontent.com"
	// ciliumWorkflow is the workflow that builds and signs the Cilium images on a release tag.
	ciliumWorkflow = "https://github.com/cilium/cilium/.github/workflows/build-images-releases.yaml@refs/tags/"
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

// CiliumIdentity is the identity of the Cilium image release build for one release tag. The regular
// expression is anchored and quotes the tag, so only that tag's workflow matches (the cosign command in
// https://docs.cilium.io/en/latest/configuration/verify-image-signatures/ names the same identity).
func CiliumIdentity(tag string) Identity {
	return Identity{
		Issuer:    githubOIDCIssuer,
		SANRegexp: "^" + regexp.QuoteMeta(ciliumWorkflow+tag) + "$",
	}
}

// Verifier resolves a tag to its digest and decides whether an image is signed by the identity. A
// signature that does not verify is an error; the callers never offer or run such an image.
type Verifier interface {
	Digest(ctx context.Context, repository, tag string) (string, error)
	Verify(ctx context.Context, img Image, id Identity) error
}

// Digest is the digest a tag of the repository resolves to, "sha256:<hex>", with a HEAD request.
func (s *Sigstore) Digest(ctx context.Context, repository, tag string) (string, error) {
	repo, err := s.repository(repository)
	if err != nil {
		return "", err
	}

	desc, err := remote.Head(repo.Tag(tag), remote.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("resolving %s:%s: %w", repository, tag, err)
	}

	return desc.Digest.String(), nil
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

	v, err := verify.NewVerifier(tr,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
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

	certID, err := verify.NewShortCertificateIdentity(id.Issuer, "", "", id.SANRegexp)
	if err != nil {
		return fmt.Errorf("the signing identity: %w", err)
	}

	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest), verify.WithCertificateIdentity(certID))

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

		if _, err := s.verifier.Verify(b, policy); err != nil {
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
