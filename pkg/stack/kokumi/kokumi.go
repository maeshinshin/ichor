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

// Package kokumi renders the pinned Kokumi release for Ichor's standard stack
// (ADR 0099): the upstream install.yaml with the registry made private and
// storage-free, the admin password replaced, and the memory limits raised.
package kokumi

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	_ "embed" // the pinned install.yaml, gzipped
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/stack/policies"
)

const (
	// Version is the pinned Kokumi release.
	Version = "0.17.0"
	// Namespace is where Kokumi runs.
	Namespace = "kokumi"
	// RegistryHost is the in-cluster address of Kokumi's registry (plain HTTP).
	RegistryHost = "kokumi-registry.kokumi.svc.cluster.local:5000"

	// DefaultRegistrySize caps the registry's emptyDir.
	DefaultRegistrySize = "1Gi"
	// memoryLimit replaces Kokumi's 128Mi, which is tight while rendering.
	memoryLimit = "256Mi"

	passwordLength = 24
	alphabet       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// installGz is install.yaml from
// https://github.com/kokumi-dev/kokumi/releases/download/0.17.0/install.yaml
// (sha256 345223726800ca3f08f633c76c91a38265bc0c131551b24dc49c69ee0cd7b840),
// gzipped with `gzip -9 -n`. `make kokumi-install` fetches and verifies it.
//
//go:embed install.yaml.gz
var installGz []byte

// Options are the inputs to Render.
type Options struct {
	// AdminPasswordHash is the bcrypt hash that replaces the shipped
	// admin/admin credential. It is an input, not generated here, because
	// bcrypt salts randomly and Render must be deterministic.
	AdminPasswordHash string
	// RegistrySize is the emptyDir size limit; DefaultRegistrySize if empty.
	RegistrySize string
	// OmitPolicies leaves the CiliumNetworkPolicies (ADR 0099 decision 10) out
	// of the output, for the first apply, which runs before they may exist.
	// The published bundle keeps them so Argo CD does not prune them.
	OmitPolicies bool
}

// Policies returns the CiliumNetworkPolicies of the kokumi namespace alone.
func Policies() []byte { return append([]byte{}, policies.Kokumi...) }

// Manifest returns the unmodified upstream install.yaml.
func Manifest() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(installGz))
	if err != nil {
		return nil, fmt.Errorf("kokumi: opening embedded install.yaml: %w", err)
	}

	defer func() { _ = zr.Close() }()

	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("kokumi: reading embedded install.yaml: %w", err)
	}

	return b, nil
}

// NewAdminPassword generates a random admin password and its bcrypt hash.
func NewAdminPassword() (password, hash string, err error) {
	buf := make([]byte, passwordLength)

	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", "", fmt.Errorf("kokumi: generating password: %w", err)
		}

		buf[i] = alphabet[n.Int64()]
	}

	h, err := bcrypt.GenerateFromPassword(buf, bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("kokumi: hashing password: %w", err)
	}

	return string(buf), string(h), nil
}

// Render returns the install manifest with Ichor's transformations applied, as
// one multi-document YAML stream in the upstream document order. A
// transformation whose target is missing is an error, so a version bump that
// renames an object cannot silently skip one.
func Render(opts Options) ([]byte, error) {
	if opts.AdminPasswordHash == "" {
		return nil, errors.New("kokumi: AdminPasswordHash is required")
	}

	if opts.RegistrySize == "" {
		opts.RegistrySize = DefaultRegistrySize
	}

	raw, err := Manifest()
	if err != nil {
		return nil, err
	}

	var (
		objs []*unstructured.Unstructured
		out  bytes.Buffer
	)

	for doc := range strings.SplitSeq(string(raw), "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}

		m := map[string]any{}
		if err := sigsyaml.Unmarshal([]byte(doc), &m); err != nil {
			return nil, fmt.Errorf("kokumi: parsing install.yaml: %w", err)
		}

		if len(m) > 0 {
			objs = append(objs, &unstructured.Unstructured{Object: m})
		}
	}

	t := &transformer{opts: opts, done: map[string]bool{}}

	kept := objs[:0]

	for _, o := range objs {
		drop, err := t.apply(o)
		if err != nil {
			return nil, err
		}

		if !drop {
			kept = append(kept, o)
		}
	}

	if err := t.complete(); err != nil {
		return nil, err
	}

	for _, o := range kept {
		b, err := sigsyaml.Marshal(o.Object)
		if err != nil {
			return nil, fmt.Errorf("kokumi: marshalling %s %s: %w", o.GetKind(), o.GetName(), err)
		}

		out.WriteString("---\n")
		out.Write(b)
	}

	if opts.OmitPolicies {
		return out.Bytes(), nil
	}

	return policies.Append(out.Bytes(), policies.Kokumi), nil
}

// transformer applies each transformation once and remembers which ran.
type transformer struct {
	opts Options
	done map[string]bool
}

var required = []string{
	"svc/kokumi-registry", "svc/kokumi-server", "pvc/kokumi-registry-pvc",
	"deploy/kokumi-registry", "deploy/kokumi-controller-manager", "deploy/kokumi-server",
	"secret/kokumi-server-auth",
}

func (t *transformer) complete() error {
	for _, k := range required {
		if !t.done[k] {
			return fmt.Errorf("kokumi: %s not found in install.yaml; the transformations need updating for this release", k)
		}
	}

	return nil
}

// apply transforms o in place; drop reports that the object is removed.
func (t *transformer) apply(o *unstructured.Unstructured) (drop bool, err error) {
	key := ""

	switch o.GetKind() {
	case "Service":
		key = "svc/"
	case "PersistentVolumeClaim":
		key = "pvc/"
	case "Deployment":
		key = "deploy/"
	case "Secret":
		key = "secret/"
	default:
		return false, nil
	}

	key += o.GetName()
	switch key {
	case "svc/kokumi-registry", "svc/kokumi-server":
		err = clusterIP(o)
	case "pvc/kokumi-registry-pvc":
		drop = true
	case "deploy/kokumi-registry":
		err = emptyDirStorage(o, t.opts.RegistrySize)
	case "deploy/kokumi-controller-manager", "deploy/kokumi-server":
		err = raiseMemory(o)
	case "secret/kokumi-server-auth":
		err = unstructured.SetNestedField(o.Object, t.opts.AdminPasswordHash, "stringData", "password-hash")
	default:
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("kokumi: transforming %s: %w", key, err)
	}

	t.done[key] = true

	return drop, nil
}

// clusterIP makes the Service ClusterIP and removes every nodePort: the
// shipped NodePort would expose an anonymous, writable registry on each node.
func clusterIP(o *unstructured.Unstructured) error {
	ports, _, err := unstructured.NestedSlice(o.Object, "spec", "ports")
	if err != nil {
		return err
	}

	for _, p := range ports {
		if pm, ok := p.(map[string]any); ok {
			delete(pm, "nodePort")
		}
	}

	if err := unstructured.SetNestedSlice(o.Object, ports, "spec", "ports"); err != nil {
		return err
	}

	return unstructured.SetNestedField(o.Object, "ClusterIP", "spec", "type")
}

// emptyDirStorage swaps the registry's PVC volume for a size-limited
// emptyDir; Talos has no StorageClass unless the user adds one.
// ponytail: use a PVC when a default StorageClass exists (ADR 0099 open question).
func emptyDirStorage(o *unstructured.Unstructured, size string) error {
	vols, _, err := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return err
	}

	found := false

	for i, v := range vols {
		vm, ok := v.(map[string]any)
		if !ok || vm["name"] != "storage" {
			continue
		}

		vols[i] = map[string]any{"name": "storage", "emptyDir": map[string]any{"sizeLimit": size}}
		found = true
	}

	if !found {
		return errors.New("volume \"storage\" not found")
	}

	return unstructured.SetNestedSlice(o.Object, vols, "spec", "template", "spec", "volumes")
}

func raiseMemory(o *unstructured.Unstructured) error {
	cs, _, err := unstructured.NestedSlice(o.Object, "spec", "template", "spec", "containers")
	if err != nil {
		return err
	}

	if len(cs) == 0 {
		return errors.New("no containers")
	}

	for _, c := range cs {
		if cm, ok := c.(map[string]any); ok {
			if err := unstructured.SetNestedField(cm, memoryLimit, "resources", "limits", "memory"); err != nil {
				return err
			}
		}
	}

	return unstructured.SetNestedSlice(o.Object, cs, "spec", "template", "spec", "containers")
}
