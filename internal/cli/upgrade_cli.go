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
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/imagesig"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/internal/version"
)

// releaseDownloadURL is where the release workflow attaches the CLI archives, the checksums and the bundle
// that signs them (ADR 0171).
const releaseDownloadURL = "https://github.com/maeshinshin/ichor/releases/download"

// binaryName is the archive member, and the name of the file the tests install.
const binaryName = "ichor"

// Limits on what the command reads: the archive is about 40 MB and the unpacked binary about 100 MB.
const (
	maxArchiveBytes = 512 << 20
	maxBinaryBytes  = 1 << 30
	maxSmallBytes   = 4 << 20
)

// blobVerifier is imagesig.Sigstore's VerifyBlob; a test gives its own.
type blobVerifier interface {
	VerifyBlob(bundleJSON []byte, artifactDigest string, id imagesig.Identity) error
}

// cliUpgrade is one run of `ichor upgrade cli`; the zero values of OS, Arch and Client are the host and
// http.DefaultClient's.
type cliUpgrade struct {
	Current  string // version.Version of the running binary
	Exe      string // the running executable
	Version  string // the VERSION argument; empty means the newest release
	Check    bool
	BaseURL  string
	OS, Arch string
	Client   *http.Client
	Latest   func() (string, error)
	Verifier blobVerifier
}

var upgradeCLICheck bool

var upgradeCLICmd = &cobra.Command{
	Use:   "cli [VERSION]",
	Short: "Replace this ichor binary with a verified release",
	Long: `Download a release of the ichor binary from its GitHub release and replace this one with it.

Without VERSION the newest release is used. The checksums file of the release is verified against its
Sigstore bundle (the identity of the release workflow of that tag), the archive against the checksums, and
only then is the binary written next to this one and renamed over it. Any mismatch stops the command;
there is no way to skip the check. A development build is never replaced. --check shows what would be
replaced and changes nothing. It asks for confirmation unless --yes.

The command reaches ghcr.io (to find the newest release) and github.com (the release assets); on a machine
that cannot, it prints the steps to do the same by hand. It does not read a cluster.`,
	Example: `  ichor upgrade cli --check
  ichor upgrade cli v0.3.0 --yes`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var want string
		if len(args) == 1 {
			want = args[0]

			if err := version.CheckSemver(want); err != nil {
				return err
			}
		}

		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("upgrade cli: finding this executable: %w", err)
		}

		v, err := imagesig.New()
		if err != nil {
			return fmt.Errorf("upgrade cli: %w", err)
		}

		u := &cliUpgrade{
			Current: version.Version, Exe: exe, Version: want, Check: upgradeCLICheck,
			Latest: func() (string, error) {
				tags, err := releasecheck.RegistryLister(false)(releasecheck.DefaultRepository)
				if err != nil {
					return "", err
				}

				return releasecheck.Latest(tags), nil
			},
			Verifier: v,
		}

		return u.run(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

func init() {
	upgradeCmd.AddCommand(upgradeCLICmd)
	upgradeCLICmd.Flags().BoolVar(&upgradeCLICheck, "check", false, "show which release would replace this binary and change nothing")
}

func (u *cliUpgrade) run(ctx context.Context, in io.Reader, out, errw io.Writer) error {
	if u.Current == "dev" {
		return errors.New("upgrade cli: this is a development build (version dev); install a release to use this command")
	}

	goos, goarch := u.OS, u.Arch
	if goos == "" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}

	if (goos != "linux" && goos != "darwin") || (goarch != "amd64" && goarch != "arm64") {
		return fmt.Errorf("upgrade cli: releases ship linux and darwin on amd64 and arm64, not %s/%s", goos, goarch)
	}

	tag, err := u.target()
	if err != nil {
		return err
	}

	if tag == u.Current {
		_, _ = fmt.Fprintf(out, "ichor is already %s\n", tag)

		return nil
	}

	exe, err := filepath.EvalSymlinks(u.Exe)
	if err != nil {
		return fmt.Errorf("upgrade cli: resolving %s: %w", u.Exe, err)
	}

	_, _ = fmt.Fprintf(out, "ichor %s at %s would be replaced by %s\n", u.Current, exe, tag)

	if u.Check {
		return nil
	}

	name := fmt.Sprintf("ichor_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)

	want, err := u.verifiedArchiveSum(ctx, tag, name)
	if err != nil {
		return err
	}

	stage, err := os.CreateTemp(filepath.Dir(exe), ".ichor-upgrade-*")
	if err != nil {
		return fmt.Errorf("upgrade cli: cannot write next to %s: %w (run it with the permissions that directory needs, or install the release by hand)", exe, err)
	}

	defer func() { _ = os.Remove(stage.Name()) }()
	defer func() { _ = stage.Close() }()

	if err := confirmPlan(in, errw, "upgrade cli", ""); err != nil {
		return err
	}

	archive, err := u.get(ctx, tag, name, maxArchiveBytes)
	if err != nil {
		return err
	}

	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return u.refuse(tag, "the archive %s does not match the signed checksums", name)
	}

	if err := unpackBinary(archive, stage); err != nil {
		return fmt.Errorf("upgrade cli: %w", err)
	}

	if err := stage.Chmod(0o755); err != nil {
		return fmt.Errorf("upgrade cli: %w", err)
	}

	if err := stage.Close(); err != nil {
		return fmt.Errorf("upgrade cli: %w", err)
	}

	if err := os.Rename(stage.Name(), exe); err != nil {
		return fmt.Errorf("upgrade cli: replacing %s: %w", exe, err)
	}

	_, _ = fmt.Fprintf(out, "ichor is now %s (%s)\n", tag, exe)

	return nil
}

// target is the release to install: VERSION, else the newest, which must be newer than this binary.
func (u *cliUpgrade) target() (string, error) {
	if u.Version != "" {
		return u.Version, nil
	}

	latest, err := u.Latest()
	if err != nil {
		return "", fmt.Errorf("upgrade cli: cannot find the newest release (%w); name one as ichor upgrade cli VERSION, or install it by hand:\n%s", err, manualSteps("vX.Y.Z"))
	}

	if latest == "" || !releasecheck.Newer(u.Current, latest) {
		return u.Current, nil
	}

	return latest, nil
}

// verifiedArchiveSum verifies the checksums file of tag against its bundle and returns the archive's sha256
// from it.
func (u *cliUpgrade) verifiedArchiveSum(ctx context.Context, tag, archive string) (string, error) {
	base := fmt.Sprintf("ichor_%s_checksums.txt", strings.TrimPrefix(tag, "v"))

	sums, err := u.get(ctx, tag, base, maxSmallBytes)
	if err != nil {
		return "", err
	}

	bundle, err := u.get(ctx, tag, base+".sigstore.json", maxSmallBytes)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(sums)
	if err := u.Verifier.VerifyBlob(bundle, "sha256:"+hex.EncodeToString(sum[:]), imagesig.IchorIdentity(tag)); err != nil {
		return "", u.refuse(tag, "the signature of %s does not verify: %v", base, err)
	}

	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == archive {
			return f[0], nil
		}
	}

	return "", u.refuse(tag, "the signed checksums do not list %s", archive)
}

func (u *cliUpgrade) refuse(tag, format string, a ...any) error {
	return fmt.Errorf("upgrade cli: refusing to install %s: %s; nothing was changed. By hand:\n%s", tag, fmt.Sprintf(format, a...), manualSteps(tag))
}

// get reads one release asset, at most limit bytes.
func (u *cliUpgrade) get(ctx context.Context, tag, name string, limit int64) ([]byte, error) {
	base := u.BaseURL
	if base == "" {
		base = releaseDownloadURL
	}

	url := base + "/" + tag + "/" + name

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("upgrade cli: %w", err)
	}

	c := u.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Minute}
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upgrade cli: cannot reach %s: %w; offline or air-gapped? Fetch the files elsewhere and install by hand:\n%s", url, err, manualSteps(tag))
	}

	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("upgrade cli: %s has no such asset (404); a release that was just tagged may still be publishing, so retry shortly", url)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("upgrade cli: %s: %s", url, resp.Status)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("upgrade cli: reading %s: %w", url, err)
	}

	if int64(len(b)) > limit {
		return nil, fmt.Errorf("upgrade cli: %s is larger than %d bytes", url, limit)
	}

	return b, nil
}

func manualSteps(tag string) string {
	v := strings.TrimPrefix(tag, "v")

	return fmt.Sprintf(`  1. download ichor_%[1]s_<os>_<arch>.tar.gz, ichor_%[1]s_checksums.txt and ichor_%[1]s_checksums.txt.sigstore.json from %[2]s/%[3]s/
  2. cosign verify-blob --bundle ichor_%[1]s_checksums.txt.sigstore.json --certificate-oidc-issuer https://token.actions.githubusercontent.com --certificate-identity https://github.com/maeshinshin/ichor/.github/workflows/release.yml@refs/tags/%[3]s ichor_%[1]s_checksums.txt
  3. sha256sum -c --ignore-missing ichor_%[1]s_checksums.txt
  4. extract ichor from the archive and copy it over this binary`, v, releaseDownloadURL, tag)
}

// unpackBinary writes the archive's ichor member to w.
func unpackBinary(archive []byte, w io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("reading the archive: %w", err)
	}

	tr := tar.NewReader(gz)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("the archive has no ichor member")
		}

		if err != nil {
			return fmt.Errorf("reading the archive: %w", err)
		}

		if h.Typeflag != tar.TypeReg || h.Name != binaryName {
			continue
		}

		n, err := io.Copy(w, io.LimitReader(tr, maxBinaryBytes+1))
		if err != nil {
			return fmt.Errorf("unpacking ichor: %w", err)
		}

		if n > maxBinaryBytes {
			return errors.New("the ichor member is implausibly large")
		}

		return nil
	}
}
