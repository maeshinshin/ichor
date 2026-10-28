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

package kokumi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
)

const hash = "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"

func render(t *testing.T) map[string]*unstructured.Unstructured {
	t.Helper()

	b, err := kokumi.Render(kokumi.Options{AdminPasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]*unstructured.Unstructured{}

	for doc := range strings.SplitSeq(string(b), "---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}

		m := map[string]any{}
		if err := sigsyaml.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatal(err)
		}

		o := &unstructured.Unstructured{Object: m}
		out[o.GetKind()+"/"+o.GetName()] = o
	}

	return out
}

func TestEmbeddedInstallMatchesThePinnedDigest(t *testing.T) {
	b, err := kokumi.Manifest()
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != "345223726800ca3f08f633c76c91a38265bc0c131551b24dc49c69ee0cd7b840" {
		t.Fatalf("install.yaml sha256 = %s", got)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a, _ := kokumi.Render(kokumi.Options{AdminPasswordHash: hash})

	b, _ := kokumi.Render(kokumi.Options{AdminPasswordHash: hash})
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Fatal("Render is not deterministic")
	}
}

func TestRenderRequiresAHash(t *testing.T) {
	if _, err := kokumi.Render(kokumi.Options{}); err == nil {
		t.Fatal("want an error without a password hash")
	}
}

func TestServicesAreClusterIPWithoutNodePorts(t *testing.T) {
	objs := render(t)

	for _, n := range []string{"Service/kokumi-registry", "Service/kokumi-server"} {
		s := objs[n]
		if s == nil {
			t.Fatalf("%s missing", n)
		}

		if ty, _, _ := unstructured.NestedString(s.Object, "spec", "type"); ty != "ClusterIP" {
			t.Errorf("%s type = %q", n, ty)
		}

		ports, _, _ := unstructured.NestedSlice(s.Object, "spec", "ports")
		for _, p := range ports {
			if _, ok := p.(map[string]any)["nodePort"]; ok {
				t.Errorf("%s still has a nodePort", n)
			}
		}
	}
}

func TestRegistryStorageIsAnEmptyDir(t *testing.T) {
	objs := render(t)

	if objs["PersistentVolumeClaim/kokumi-registry-pvc"] != nil {
		t.Error("the PVC is still rendered")
	}

	dep := objs["Deployment/kokumi-registry"]
	vols, _, _ := unstructured.NestedSlice(dep.Object, "spec", "template", "spec", "volumes")

	for _, v := range vols {
		vm := v.(map[string]any)
		if vm["name"] != "storage" {
			continue
		}

		if _, ok := vm["persistentVolumeClaim"]; ok {
			t.Error("storage still uses a PVC")
		}

		if lim, _, _ := unstructured.NestedString(vm, "emptyDir", "sizeLimit"); lim != kokumi.DefaultRegistrySize {
			t.Errorf("sizeLimit = %q", lim)
		}

		return
	}

	t.Fatal("storage volume missing")
}

func TestMemoryLimitsAreRaised(t *testing.T) {
	objs := render(t)

	for _, n := range []string{"Deployment/kokumi-controller-manager", "Deployment/kokumi-server"} {
		cs, _, _ := unstructured.NestedSlice(objs[n].Object, "spec", "template", "spec", "containers")

		mem, _, _ := unstructured.NestedString(cs[0].(map[string]any), "resources", "limits", "memory")
		if mem != "256Mi" {
			t.Errorf("%s memory limit = %q", n, mem)
		}
	}
}

func TestAdminPasswordIsReplaced(t *testing.T) {
	s := render(t)["Secret/kokumi-server-auth"]

	if got, _, _ := unstructured.NestedString(s.Object, "stringData", "password-hash"); got != hash {
		t.Fatalf("password-hash = %q", got)
	}

	// The shipped admin/admin hash must be gone from the whole stream.
	raw, _ := kokumi.Render(kokumi.Options{AdminPasswordHash: hash})
	if bytes.Contains(raw, []byte("$2y$05$MG5FZ")) {
		t.Fatal("the shipped admin hash is still present")
	}
}

func TestNewAdminPasswordHashesThePassword(t *testing.T) {
	pw, h, err := kokumi.NewAdminPassword()
	if err != nil {
		t.Fatal(err)
	}

	if len(pw) < 20 || bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) != nil {
		t.Fatalf("password %q does not match hash", pw)
	}
}
