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

// Package stack builds and publishes the OCI bundles Kokumi turns into Argo CD
// Applications. The renderers live in the argocd and
// kokumi subpackages; this package only packs their output and pushes it.
package stack

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	// LayerMediaType is the single layer's type: a gzipped tar of the files,
	// which Kokumi's Order reads as a pre-rendered manifest bundle.
	LayerMediaType types.MediaType = "application/vnd.cncf.flux.content.v1.tar+gzip"
	// ConfigMediaType is the artifact's config type (an empty JSON object).
	ConfigMediaType types.MediaType = "application/vnd.cncf.flux.config.v1+json"
	// ManifestFile is the name of the one file in the bundle.
	ManifestFile = "manifest.yaml"

	hashLen = 12
)

// Tag returns "<version>-<hash12>", where the hash covers the rendered bytes,
// so the same input always names the same artifact.
func Tag(version string, manifest []byte) string {
	sum := sha256.Sum256(manifest)

	return version + "-" + hex.EncodeToString(sum[:])[:hashLen]
}

// Tar packs manifest as ManifestFile into a gzipped tar that is a pure
// function of its input: zero mtimes, fixed owner and mode, no gzip name or
// time. The bytes are stable for a given Go compress/gzip implementation.
func Tar(manifest []byte) ([]byte, error) {
	var buf bytes.Buffer

	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("stack: creating gzip writer: %w", err)
	}

	tw := tar.NewWriter(zw)

	hdr := &tar.Header{
		Typeflag: tar.TypeReg, Name: ManifestFile, Mode: 0o644, Size: int64(len(manifest)),
		ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, fmt.Errorf("stack: writing tar header: %w", err)
	}

	if _, err := tw.Write(manifest); err != nil {
		return nil, fmt.Errorf("stack: writing tar body: %w", err)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("stack: closing tar: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("stack: closing gzip: %w", err)
	}

	return buf.Bytes(), nil
}

// Image builds the single-layer OCI artifact for manifest. annotations (for
// example org.opencontainers.image.source and .version) go on the manifest.
func Image(manifest []byte, annotations map[string]string) (v1.Image, error) {
	layer, err := Tar(manifest)
	if err != nil {
		return nil, err
	}

	img := mutate.ConfigMediaType(mutate.MediaType(empty.Image, types.OCIManifestSchema1), ConfigMediaType)

	img, err = mutate.Append(img, mutate.Addendum{Layer: static.NewLayer(layer, LayerMediaType)})
	if err != nil {
		return nil, fmt.Errorf("stack: building image: %w", err)
	}

	if len(annotations) > 0 {
		// Annotations on an Image returns an Image.
		img = mutate.Annotations(img, annotations).(v1.Image) //nolint:errcheck,forcetypeassert
	}

	return img, nil
}

// Push publishes manifest as repository:tag over plain HTTP (Kokumi's registry
// has no TLS and no auth) and returns the manifest digest. Pushing
// the same input twice yields the same digest. Extra remote options (a
// transport, say) are passed through.
func Push(
	ctx context.Context, repository, tag string, manifest []byte, annotations map[string]string, opts ...remote.Option,
) (string, error) {
	ref, err := name.NewTag(repository+":"+tag, name.Insecure)
	if err != nil {
		return "", fmt.Errorf("stack: reference %s:%s: %w", repository, tag, err)
	}

	img, err := Image(manifest, annotations)
	if err != nil {
		return "", err
	}

	if err := remote.Write(ref, img, append([]remote.Option{remote.WithContext(ctx)}, opts...)...); err != nil {
		return "", fmt.Errorf("stack: pushing %s: %w", ref, err)
	}

	d, err := img.Digest()
	if err != nil {
		return "", fmt.Errorf("stack: digest of %s: %w", ref, err)
	}

	return d.String(), nil
}

// SplitSecrets separates the rendered manifest into the bundle that may be published and the
// Secrets that must not be. Kokumi's registry is anonymous, so anything in a bundle is readable
// by every pod that can reach it; a Secret is created directly in its
// namespace instead. The bundle keeps the order and bytes of every other document.
func SplitSecrets(manifest []byte) (bundle []byte, secrets []*unstructured.Unstructured, err error) {
	rd := yamlutil.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifest)))

	var out bytes.Buffer

	for {
		doc, err := rd.Read()
		if errors.Is(err, io.EOF) {
			return out.Bytes(), secrets, nil
		}

		if err != nil {
			return nil, nil, fmt.Errorf("stack: splitting the manifest: %w", err)
		}

		obj := map[string]any{}
		if err := sigsyaml.Unmarshal(doc, &obj); err != nil {
			return nil, nil, fmt.Errorf("stack: parsing the manifest: %w", err)
		}

		if len(obj) == 0 {
			continue
		}

		if u := (&unstructured.Unstructured{Object: obj}); u.GetKind() == "Secret" && u.GroupVersionKind().Group == "" {
			secrets = append(secrets, u)

			continue
		}

		out.WriteString("---\n")
		out.Write(bytes.TrimSpace(doc))
		out.WriteString("\n")
	}
}
