package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireNotice fails unless text carries the copyright and licence lines the upstream file opens with.
func requireNotice(t *testing.T, name, text string) {
	t.Helper()

	for _, want := range []string{"# Copyright The Kubernetes Authors.", "Licensed under the Apache License, Version 2.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks the upstream notice line %q", name, want)
		}
	}
}

// TestCommittedCRDsKeepUpstreamNotice fails if the embedded CRDs lose the notice Apache-2.0 section 4(c) requires.
func TestCommittedCRDsKeepUpstreamNotice(t *testing.T) {
	b, err := os.ReadFile("../../../pkg/cni/gatewayapi/crds.yaml")
	if err != nil {
		t.Fatal(err)
	}

	requireNotice(t, "pkg/cni/gatewayapi/crds.yaml", string(b))
}

// TestExtractCarriesUpstreamNotice runs extract on a small upstream whose first document is the notice.
func TestExtractCarriesUpstreamNotice(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.yaml"), filepath.Join(dir, "out.yaml")

	upstream := "# Copyright The Kubernetes Authors.\n#\n# Licensed under the Apache License, Version 2.0 (the \"License\");\n#\n" +
		"# Gateway API Standard channel install\n#\n---\n" +
		"apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: gateways.gateway.networking.k8s.io\n"
	if err := os.WriteFile(in, []byte(upstream), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extract(in, out, "gateways", "v0.0.0"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	requireNotice(t, "the extracted file", string(b))

	if !strings.Contains(string(b), "name: gateways.gateway.networking.k8s.io") {
		t.Errorf("the extracted file lost the CRD:\n%s", b)
	}
}

// TestExtractRefusesUpstreamWithoutNotice keeps the tool from writing CRDs whose source carries no notice.
func TestExtractRefusesUpstreamWithoutNotice(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.yaml")

	upstream := "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: gateways.gateway.networking.k8s.io\n"
	if err := os.WriteFile(in, []byte(upstream), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extract(in, filepath.Join(dir, "out.yaml"), "gateways", "v0.0.0"); err == nil {
		t.Error("extract accepted an upstream with no copyright notice")
	}
}
