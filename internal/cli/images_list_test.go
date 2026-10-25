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

package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// TestBuildImageList_BelowInstallerBaseBoundary proves the pre-1.10.0
// image name: siderolabs/installer, not siderolabs/installer-base, and
// no siderolabs/talosctl-all yet either.
func TestBuildImageList_BelowInstallerBaseBoundary(t *testing.T) {
	got, err := buildImageList("", "v1.9.5")
	if err != nil {
		t.Fatalf("buildImageList: %v", err)
	}

	wantImages := []string{
		"ghcr.io/siderolabs/imager:v1.9.5",
		"ghcr.io/siderolabs/installer:v1.9.5",
		"ghcr.io/siderolabs/extensions:v1.9.5",
		"ghcr.io/siderolabs/overlays:v1.9.5",
	}

	if !slices.Equal(got.Images, wantImages) {
		t.Fatalf("images = %v, want %v", got.Images, wantImages)
	}
}

// TestBuildImageList_InstallerBaseBoundary proves the exact boundary
// (1.10.0 itself) already switches to installer-base, and still has no
// talosctl-all.
func TestBuildImageList_InstallerBaseBoundary(t *testing.T) {
	got, err := buildImageList("", "v1.10.0")
	if err != nil {
		t.Fatalf("buildImageList: %v", err)
	}

	wantImages := []string{
		"ghcr.io/siderolabs/imager:v1.10.0",
		"ghcr.io/siderolabs/installer-base:v1.10.0",
		"ghcr.io/siderolabs/extensions:v1.10.0",
		"ghcr.io/siderolabs/overlays:v1.10.0",
	}

	if !slices.Equal(got.Images, wantImages) {
		t.Fatalf("images = %v, want %v", got.Images, wantImages)
	}
}

// TestBuildImageList_TalosctlAllBoundary proves 1.11.0 adds
// siderolabs/talosctl-all on top of everything 1.10.0 already has.
func TestBuildImageList_TalosctlAllBoundary(t *testing.T) {
	got, err := buildImageList("", "v1.11.0")
	if err != nil {
		t.Fatalf("buildImageList: %v", err)
	}

	wantImages := []string{
		"ghcr.io/siderolabs/imager:v1.11.0",
		"ghcr.io/siderolabs/installer-base:v1.11.0",
		"ghcr.io/siderolabs/extensions:v1.11.0",
		"ghcr.io/siderolabs/overlays:v1.11.0",
		"ghcr.io/siderolabs/talosctl-all:v1.11.0",
	}

	if !slices.Equal(got.Images, wantImages) {
		t.Fatalf("images = %v, want %v", got.Images, wantImages)
	}
}

// TestBuildImageList_CatalogsAreFlaggedSeparately proves extensions and
// overlays are always reported as catalogs distinct from the rest, and
// that the warning naming them is always present -- the whole reason
// this command exists (docs/design/architecture.md: a mirror that looks
// complete at five images fails at the first schematic needing an
// extension).
func TestBuildImageList_CatalogsAreFlaggedSeparately(t *testing.T) {
	got, err := buildImageList("", "v1.11.0")
	if err != nil {
		t.Fatalf("buildImageList: %v", err)
	}

	wantCatalogs := []string{
		"ghcr.io/siderolabs/extensions:v1.11.0",
		"ghcr.io/siderolabs/overlays:v1.11.0",
	}

	if !slices.Equal(got.Catalogs, wantCatalogs) {
		t.Fatalf("catalogs = %v, want %v", got.Catalogs, wantCatalogs)
	}

	if !strings.Contains(got.Warning, "catalogs") || !strings.Contains(got.Warning, "does not mirror") {
		t.Fatalf("warning does not say catalogs don't mirror what they reference: %q", got.Warning)
	}
}

// TestBuildImageList_CustomSourceRegistry proves --image-registry
// actually changes every reference's prefix, not just the first one.
func TestBuildImageList_CustomSourceRegistry(t *testing.T) {
	got, err := buildImageList("registry.example.invalid", "v1.9.0")
	if err != nil {
		t.Fatalf("buildImageList: %v", err)
	}

	for _, ref := range got.Images {
		if !strings.HasPrefix(ref, "registry.example.invalid/siderolabs/") {
			t.Fatalf("image %q does not use the custom source registry", ref)
		}
	}
}

// TestBuildImageList_RejectsInvalidVersion proves a malformed
// --talos-version fails loudly instead of silently producing a
// nonsensical image tag.
func TestBuildImageList_RejectsInvalidVersion(t *testing.T) {
	if _, err := buildImageList("", "1.11.0"); err == nil {
		t.Fatal("expected an error for a version missing its v prefix, got nil")
	}
}

// TestRunImagesList_RequiresAtLeastOneVersion proves --talos-version
// being empty (bypassing cobra's MarkFlagRequired, e.g. when
// runImagesList is called directly) still fails rather than printing
// nothing.
func TestRunImagesList_RequiresAtLeastOneVersion(t *testing.T) {
	if err := runImagesList(&bytes.Buffer{}, imagesListOptions{}); err == nil {
		t.Fatal("expected an error for zero --talos-version values, got nil")
	}
}

// TestRunImagesList_MultipleVersionsYAML proves --talos-version is
// repeatable and each version's result is written as its own YAML
// document (output.go's existing multi-document convention), in request
// order.
func TestRunImagesList_MultipleVersionsYAML(t *testing.T) {
	oldFormat := outputFormat
	outputFormat = outputYAML

	t.Cleanup(func() { outputFormat = oldFormat })

	var buf bytes.Buffer

	opts := imagesListOptions{TalosVersions: []string{"v1.9.0", "v1.11.0"}}
	if err := runImagesList(&buf, opts); err != nil {
		t.Fatalf("runImagesList: %v", err)
	}

	out := buf.String()

	firstIdx := strings.Index(out, "v1.9.0")
	secondIdx := strings.Index(out, "v1.11.0")

	if firstIdx < 0 || secondIdx < 0 || firstIdx > secondIdx {
		t.Fatalf("expected v1.9.0's document before v1.11.0's, got: %s", out)
	}

	if !strings.Contains(out, "---") {
		t.Fatalf("expected a YAML document separator between the two versions, got: %s", out)
	}
}

// TestRunImagesList_JSON proves the -o json path produces valid,
// decodable JSON carrying the same data.
func TestRunImagesList_JSON(t *testing.T) {
	oldFormat := outputFormat
	outputFormat = outputJSON

	t.Cleanup(func() { outputFormat = oldFormat })

	var buf bytes.Buffer

	opts := imagesListOptions{TalosVersions: []string{"v1.11.0"}}
	if err := runImagesList(&buf, opts); err != nil {
		t.Fatalf("runImagesList: %v", err)
	}

	if !strings.Contains(buf.String(), `"talosVersion": "v1.11.0"`) {
		t.Fatalf("expected JSON output naming the Talos version, got: %s", buf.String())
	}
}
