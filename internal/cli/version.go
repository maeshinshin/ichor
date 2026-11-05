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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/release"
)

// versionTimeout bounds the cluster read: an unreachable API server must not hang a version check.
const versionTimeout = 5 * time.Second

var versionClientOnly bool

// clientVersion is the CLI's stamped version, or the module's build info for an unstamped build.
func clientVersion() string {
	if version.Version != "dev" {
		return version.Version
	}

	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}

	return version.Version
}

type versionInfo struct {
	Client  versionClient   `json:"client"`
	Release *versionRelease `json:"newInstallVersions,omitempty"`
	Server  *versionServer  `json:"server,omitempty"`
}

type versionClient struct {
	Version string `json:"version"`
}

// versionRelease is the set of versions a new install gets from this Ichor build: the first install's defaults. An
// upgrade is not limited to it.
type versionRelease struct {
	Talos           string `json:"talosVersion"`
	VersionContract string `json:"versionContract"`
	Kubernetes      string `json:"kubernetesVersion"`
	Cilium          string `json:"ciliumChartVersion"`
}

// releaseOf is the release set of this build.
func releaseOf() *versionRelease {
	p := release.Pinned

	return &versionRelease{Talos: p.Talos, VersionContract: p.VersionContract, Kubernetes: p.Kubernetes, Cilium: p.Cilium}
}

type versionServer struct {
	InstalledVersion string `json:"installedVersion,omitempty"`
	// SpecVersion is set only when IchorInstallation.spec.version differs from what is installed.
	SpecVersion string `json:"specVersion,omitempty"`
	// LatestRelease is the newer Ichor release the cluster's controller found; empty when none is newer.
	LatestRelease string `json:"latestRelease,omitempty"`
	// InstallerImage is the installer image the last Ichor upgrade ran and whether its signature was checked.
	InstallerImage *versionInstaller `json:"installerImage,omitempty"`
}

// versionInstaller is IchorInstallation.status.installerImage, plus whether the next upgrade will skip the check.
type versionInstaller struct {
	Digest       string `json:"digest,omitempty"`
	Verification string `json:"verification,omitempty"`
	// SkipNext is true while spec.installerSignature says skip: the next upgrade will not check the signature.
	SkipNext bool `json:"skipNext,omitempty"`
}

// collectVersion reads the IchorInstallation through c (nil means --client). The returned note is
// non-empty when the server half is missing; it is not an error.
func collectVersion(ctx context.Context, c client.Reader, cliVersion string) (info versionInfo, note string) {
	info.Client.Version = cliVersion
	info.Release = releaseOf()

	if c == nil {
		return info, ""
	}

	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()

	var inst ichorv1alpha1.IchorInstallation
	if err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		return info, fmt.Sprintf("installed Ichor version unavailable: %v", err)
	}

	info.Server = &versionServer{InstalledVersion: inst.Status.InstalledVersion}
	if inst.Spec.Version != inst.Status.InstalledVersion {
		info.Server.SpecVersion = inst.Spec.Version
	}

	if rel := inst.Status.LatestRelease; rel != nil && releasecheck.Newer(inst.Status.InstalledVersion, rel.Version) {
		info.Server.LatestRelease = rel.Version
	}

	skipNext := inst.Spec.InstallerSignature != nil && inst.Spec.InstallerSignature.Mode == ichorv1alpha1.InstallerSignatureSkip
	if img := inst.Status.InstallerImage; img != nil {
		info.Server.InstallerImage = &versionInstaller{Digest: img.Digest, Verification: img.Verification, SkipNext: skipNext}
	} else if skipNext {
		info.Server.InstallerImage = &versionInstaller{SkipNext: true}
	}

	return info, ""
}

// labelIchor is the CLI line of `ichor version`.
const labelIchor = "ichor"

// writeVersionHuman prints what a person wants from `ichor version`: the CLI, the installed Ichor,
// an upgrade or newer release when there is one, and what a new install would get.
func writeVersionHuman(w io.Writer, info versionInfo) error {
	rows := []kv{{labelIchor, info.Client.Version}}

	if s := info.Server; s != nil {
		rows = append(rows, kv{"installed", s.InstalledVersion}, kv{"upgrading", s.SpecVersion})

		if s.LatestRelease != "" {
			rows = append(rows, kv{"latest", s.LatestRelease + " available"})
		}

		if img := s.InstallerImage; img != nil {
			rows = append(rows, kv{"installer image", installerImageText(img)})
		}
	}

	if r := info.Release; r != nil {
		rows = append(rows, kv{"new installs", fmt.Sprintf("Talos %s · Kubernetes %s · Cilium %s (contract %s)",
			r.Talos, r.Kubernetes, r.Cilium, r.VersionContract)})
	}

	h := newHuman(w)
	h.Block("", rows...)

	return h.Err()
}

// installerImageText says what `ichor version` shows about the installer image: verified, or loudly not.
func installerImageText(img *versionInstaller) string {
	var text string

	switch {
	case img.Verification == ichorv1alpha1.InstallerImageSkipped:
		text = "signature NOT checked"
	case img.Verification != "":
		text = "signature verified"
	}

	if img.Digest != "" {
		text += " " + img.Digest
	}

	if img.SkipNext {
		const skip = "the next upgrade will NOT check the signature"

		// With no installer Job run yet there is no image to describe: say only the warning.
		if text = strings.TrimSpace(text); text != "" {
			text += "; "
		}

		text += skip
	}

	return strings.TrimSpace(text)
}

func writeVersion(w io.Writer, format string, info versionInfo) error {
	if format == outputHuman {
		return writeVersionHuman(w, info)
	}

	var (
		out []byte
		err error
	)

	if format == outputJSON {
		out, err = json.MarshalIndent(info, "", "  ")
		out = append(out, '\n')
	} else {
		out, err = sigsyaml.Marshal(info)
	}

	if err != nil {
		return err
	}

	_, err = w.Write(out)

	return err
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the CLI version and the installed Ichor version",
	Long: `Print this CLI's version and, when the cluster is reachable, the version of Ichor
installed there (IchorInstallation status.installedVersion, plus spec.version when
it differs, which means an upgrade is pending). When the cluster cannot be
reached, the CLI version is printed with a note on stderr and the exit code is 0.
--client skips the cluster.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateOutputFormat(outputFormat); err != nil {
			return err
		}

		var reader client.Reader

		var note string

		if !versionClientOnly {
			c, err := newKubeClient(kubeconfigPath)
			if err != nil {
				note = fmt.Sprintf("installed Ichor version unavailable: %v", err)
			} else {
				reader = c
			}
		}

		info, getNote := collectVersion(cmd.Context(), reader, clientVersion())
		if note == "" {
			note = getNote
		}

		if note != "" {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), note)
		}

		return writeVersion(cmd.OutOrStdout(), outputFormat, info)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolVar(&versionClientOnly, "client", false, "print only the CLI version; do not contact the cluster")
}
