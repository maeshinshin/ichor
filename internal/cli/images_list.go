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
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
	sigsyaml "sigs.k8s.io/yaml"
)

// imageFactoryRegistry is the default source registry Image Factory's own
// images come from (docs/design/architecture.md, "Monitoring and air
// gap": "It pulls from ghcr.io unless given -image-registry").
const imageFactoryRegistry = "ghcr.io"

// installerBaseMinVersion and talosctlAllMinVersion are the two version
// boundaries hack/copy-artifacts.sh encodes only as a comment (verified
// against the Image Factory repo's own script and docs/air-gapped.md,
// 2026-10-26): siderolabs/installer-base replaces siderolabs/installer
// at Talos 1.10.0, and siderolabs/talosctl-all does not exist before
// 1.11.0.
const (
	installerBaseMinVersion = "v1.10.0"
	talosctlAllMinVersion   = "v1.11.0"
)

// catalogWarning is printed whenever imagesList's output could be
// mistaken for a complete mirror (docs/design/architecture.md: "A mirror
// that holds the five images above and nothing else looks complete and
// fails at the first schematic that asks for an extension").
// imageList never resolves a catalog's or a schematic's contents itself
// -- see imagesListOptions.Schematic's doc comment for why -- so this
// warning fires unconditionally, not only when --schematic is given: a
// plain --talos-version call gives no way to know whether some other
// cluster's schematic will need an extension this list doesn't name.
const catalogWarning = "siderolabs/extensions and siderolabs/overlays are catalogs, not the " +
	"images they reference: mirroring them does not mirror the extension/overlay images a " +
	"schematic actually names, and those need the same registry namespace prefix the catalogs " +
	"do. Image Factory has no API that resolves a schematic ID into concrete image references " +
	"without reading the schematic and then the catalogs themselves (network calls this command " +
	"deliberately never makes); resolve and mirror them separately, e.g. with hack/copy-artifacts.sh."

// imageEntry is one image imagesList names.
type imageEntry struct {
	// Name is the siderolabs/<name> path, without a registry or tag.
	Name string
	// Catalog is true for siderolabs/extensions and siderolabs/overlays:
	// an index of other images, not a usable image on its own.
	Catalog bool
}

// talosImages returns, in a fixed order, every Image Factory image
// Talos talosVersion needs mirrored (docs/design/architecture.md,
// "Monitoring and air gap", sourced from the Factory's own docs/sources.md
// and docs/air-gapped.md): imager and installer-base (installer below
// 1.10.0) and the two catalogs always; talosctl-all from 1.11.0 on.
func talosImages(talosVersion string) []imageEntry {
	images := []imageEntry{{Name: "siderolabs/imager"}}

	if semver.Compare(talosVersion, installerBaseMinVersion) >= 0 {
		images = append(images, imageEntry{Name: "siderolabs/installer-base"})
	} else {
		images = append(images, imageEntry{Name: "siderolabs/installer"})
	}

	images = append(images,
		imageEntry{Name: "siderolabs/extensions", Catalog: true},
		imageEntry{Name: "siderolabs/overlays", Catalog: true},
	)

	if semver.Compare(talosVersion, talosctlAllMinVersion) >= 0 {
		images = append(images, imageEntry{Name: "siderolabs/talosctl-all"})
	}

	return images
}

// imageListResult is imagesListOptions' output for one Talos version.
type imageListResult struct {
	TalosVersion string   `json:"talosVersion" yaml:"talosVersion"`
	Images       []string `json:"images" yaml:"images"`
	Catalogs     []string `json:"catalogs" yaml:"catalogs"`
	Warning      string   `json:"warning" yaml:"warning"`
}

// buildImageList resolves talosVersion against sourceRegistry (empty
// defaults to imageFactoryRegistry) into an imageListResult. It never
// dials anything -- registry or Image Factory -- so a result is always
// just the version-keyed image names and tags this package already
// knows, never anything that could fail to reach a network.
func buildImageList(sourceRegistry, talosVersion string) (imageListResult, error) {
	if !semver.IsValid(talosVersion) {
		return imageListResult{}, fmt.Errorf("images list: invalid --talos-version %q: want vX.Y.Z", talosVersion)
	}

	if sourceRegistry == "" {
		sourceRegistry = imageFactoryRegistry
	}

	result := imageListResult{TalosVersion: talosVersion, Warning: catalogWarning}

	for _, img := range talosImages(talosVersion) {
		ref := fmt.Sprintf("%s/%s:%s", sourceRegistry, img.Name, talosVersion)

		result.Images = append(result.Images, ref)

		if img.Catalog {
			result.Catalogs = append(result.Catalogs, ref)
		}
	}

	return result, nil
}

// imagesListOptions is imagesListCmd's flags.
type imagesListOptions struct {
	TalosVersions  []string
	SourceRegistry string

	// Schematic is accepted (docs/design/architecture.md names "a given
	// version set" as Talos version plus schematic) but never resolved:
	// a TalosClusterSchematic stores only Image Factory's opaque
	// schematic ID (api/v1alpha1/taloscluster_types.go), and resolving
	// it into the extension/overlay images it actually names needs two
	// network calls Image Factory's own API requires (GET
	// /schematics/:id to read the schematic, then reading the
	// extensions/overlays catalog images to resolve each reference --
	// confirmed against the Image Factory repo's own docs/api.md and
	// docs/sources.md, 2026-10-26: there is no single endpoint that
	// returns a schematic's resolved image list). This command never
	// connects to anything, so it cannot do either
	Schematic string
}

// imagesListCmd implements `ichor images list`
// (docs/design/architecture.md, "Monitoring and air gap": "`ichor images
// list` to print everything that needs mirroring for a given version
// set"). It never connects to a registry or to Image Factory -- every
// value it prints comes from this package's own version-keyed knowledge
// (talosImages) -- so mirror reachability (the design's separate
// pre-check) is explicitly not this command's job.
var imagesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the images one or more Talos versions need mirrored for an air-gapped install",
	Long: `list prints, for each --talos-version, the Image Factory images that need to
be mirrored into an air-gapped registry: siderolabs/imager,
siderolabs/installer-base (siderolabs/installer below Talos 1.10.0),
siderolabs/extensions, siderolabs/overlays, and siderolabs/talosctl-all
(Talos 1.11.0 and up).

siderolabs/extensions and siderolabs/overlays are catalogs: mirroring them
does not mirror what they reference. Every extension/overlay image a
schematic actually names must be mirrored separately -- this command
cannot resolve that without connecting to a registry or to Image Factory,
which it deliberately never does -- and the output
always says so.

Pass --talos-version more than once to cover an upgrade's "from" and "to"
versions in one call.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateOutputFormat(outputFormat); err != nil {
			return err
		}

		return runImagesList(cmd.OutOrStdout(), imagesListOpts)
	},
}

var imagesListOpts imagesListOptions

func init() {
	imagesCmd.AddCommand(imagesListCmd)

	flags := imagesListCmd.Flags()
	flags.StringArrayVar(&imagesListOpts.TalosVersions, "talos-version", nil,
		"Talos version to list images for (vX.Y.Z; repeatable, required)")
	flags.StringVar(&imagesListOpts.SourceRegistry, "image-registry", imageFactoryRegistry,
		"registry to prefix every image reference with")
	flags.StringVar(&imagesListOpts.Schematic, "schematic", "", "Image Factory schematic ID in use (named in the output only; see Long)")

	_ = imagesListCmd.MarkFlagRequired("talos-version")
}

// runImagesList is imagesListCmd's RunE, factored out so a test can call
// it directly.
func runImagesList(w io.Writer, opts imagesListOptions) error {
	if len(opts.TalosVersions) == 0 {
		return fmt.Errorf("images list: --talos-version is required")
	}

	results := make([]imageListResult, 0, len(opts.TalosVersions))

	for _, v := range opts.TalosVersions {
		result, err := buildImageList(opts.SourceRegistry, v)
		if err != nil {
			return err
		}

		results = append(results, result)
	}

	return writeImageLists(w, outputFormat, results)
}

// writeImageLists marshals results as YAML or JSON, matching the CLI's
// existing -o yaml|json convention (output.go's writeOutput/
// writeOutputObjects) without reusing either: neither takes a plain Go
// value, only client.Object/TalosConfigPatch, and imageListResult is
// neither.
func writeImageLists(w io.Writer, format string, results []imageListResult) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		return enc.Encode(results)
	case outputYAML:
		for i := range results {
			if i > 0 {
				if _, err := fmt.Fprintln(w, "---"); err != nil {
					return err
				}
			}

			out, err := sigsyaml.Marshal(&results[i])
			if err != nil {
				return fmt.Errorf("images list: encoding output as YAML: %w", err)
			}

			if _, err := w.Write(out); err != nil {
				return err
			}
		}

		return nil
	default:
		return fmt.Errorf("images list: invalid --output %q", format)
	}
}
