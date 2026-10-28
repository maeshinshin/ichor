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

package stack_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/maeshinshin/ichor/pkg/stack"
	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
)

func TestTarIsDeterministicAndHoldsTheManifest(t *testing.T) {
	m := []byte("kind: ConfigMap\n")

	a, err := stack.Tar(m)
	if err != nil {
		t.Fatal(err)
	}

	b, _ := stack.Tar(m)
	if !bytes.Equal(a, b) {
		t.Fatal("Tar is not deterministic")
	}

	zr, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(zr)

	h, err := tr.Next()
	if err != nil || h.Name != stack.ManifestFile || h.ModTime.Unix() != 0 || h.Uid != 0 {
		t.Fatalf("header = %+v, err %v", h, err)
	}

	got, _ := io.ReadAll(tr)
	if !bytes.Equal(got, m) {
		t.Fatalf("content = %q", got)
	}

	if _, err := tr.Next(); err != io.EOF {
		t.Fatalf("want a single entry, next err = %v", err)
	}
}

func TestTagCoversTheBytes(t *testing.T) {
	a, b := stack.Tag("1.0", []byte("a")), stack.Tag("1.0", []byte("b"))
	if a == b || !strings.HasPrefix(a, "1.0-") || len(a) != len("1.0-")+12 {
		t.Fatalf("tags %q %q", a, b)
	}
}

func TestPushToInProcessRegistry(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()

	repo := strings.TrimPrefix(srv.URL, "http://") + "/ichor/argocd"
	m := []byte("apiVersion: v1\nkind: ConfigMap\n")
	tag := stack.Tag("10.11.6", m)
	ann := map[string]string{"org.opencontainers.image.version": "10.11.6"}

	d1, err := stack.Push(t.Context(), repo, tag, m, ann)
	if err != nil {
		t.Fatal(err)
	}

	d2, err := stack.Push(t.Context(), repo, tag, m, ann)
	if err != nil || d1 != d2 {
		t.Fatalf("second push digest %q err %v, want %q", d2, err, d1)
	}

	ref, _ := name.NewTag(repo+":"+tag, name.Insecure)

	img, err := remote.Image(ref)
	if err != nil {
		t.Fatal(err)
	}

	man, _ := img.Manifest()
	if len(man.Layers) != 1 || man.Layers[0].MediaType != stack.LayerMediaType {
		t.Fatalf("layers = %+v", man.Layers)
	}

	if man.Annotations["org.opencontainers.image.version"] != "10.11.6" {
		t.Fatalf("annotations = %v", man.Annotations)
	}

	if dg, _ := img.Digest(); dg.String() != d1 {
		t.Fatalf("pulled digest %s != pushed %s", dg, d1)
	}
}

func TestRepositoryIsInsideKokumiRegistry(t *testing.T) {
	if got, want := stack.Repository("argocd"), kokumi.RegistryHost+"/ichor/argocd"; got != want {
		t.Fatalf("%q != %q", got, want)
	}
}
