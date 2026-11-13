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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// secretsCmd groups the commands on the local secrets bundle file. They touch no cluster.
var secretsCmd = &cobra.Command{
	Use:     "secrets",
	GroupID: groupAccess,
	Short:   "Manage the local secrets bundle file",
	Example: `  ichor secrets encrypt prod`,
}

var secretsEncryptFile string

var secretsEncryptCmd = &cobra.Command{
	Use:   "encrypt [CLUSTER]",
	Short: "Encrypt a plaintext secrets bundle file in place",
	Long: `encrypt seals a plaintext secrets bundle as an age file under a passphrase, replacing the file
atomically and only after the sealed copy has been opened again and found identical. Name the cluster to
encrypt ~/.ichor/clusters/<CLUSTER>/secrets.yaml, or give --file for any other bundle file (for example one
made by 'talosctl gen secrets'). A file that is already encrypted is left alone.

The passphrase comes from --bundle-passphrase-file or $ICHOR_BUNDLE_PASSPHRASE; on a terminal with neither,
one is generated and printed once. The bundle is never printed. The old plaintext may remain on the disk until
its blocks are overwritten; if the file was ever exposed, rotate the CAs.

An encrypted file is a standard age file: 'age -d secrets.yaml' opens it.`,
	Example: `  ichor secrets encrypt prod
  ichor secrets encrypt --file ./secrets.yaml`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := secretsEncryptFile

		switch {
		case path != "" && len(args) == 1:
			return errors.New("give a cluster name or --file, not both")
		case path == "" && len(args) == 0:
			return errors.New("give a cluster name or --file")
		case path == "":
			dir, err := bundleDir(args[0])
			if err != nil {
				return err
			}

			path = filepath.Join(dir, secretsbundle.SecretKey)
		}

		return runSecretsEncrypt(path, cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

// runSecretsEncrypt is the migration. Nothing is replaced unless the sealed bytes open back
// to the original.
func runSecretsEncrypt(path string, stdout, stderr io.Writer) error {
	plain, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading secrets bundle %q: %w", path, err)
	}

	if isEncryptedBundle(plain) {
		_, err = fmt.Fprintf(stdout, "%s is already encrypted; nothing to do.\n", path)

		return err
	}

	// Refuse something that is not a bundle, so a wrong --file is not turned into an unreadable file.
	b, err := secretsbundle.Decode(plain)
	if err != nil {
		return fmt.Errorf("%q is not a secrets bundle: %w", path, err)
	}

	if b.Cluster == nil || b.Cluster.ID == "" || b.Secrets == nil {
		return fmt.Errorf("%q is not a secrets bundle: it has no cluster or secrets section", path)
	}

	pass, generated, err := resolveSealPassphrase(false)
	if errors.Is(err, errNoSealPassphrase) {
		return errNoSealPassphraseOnly // this command has no --bundle-plaintext to offer
	}

	if err != nil {
		return err
	}

	sealed, err := sealBundleBytes(plain, pass)
	if err != nil {
		return err
	}

	if back, err := openBundleBytes(sealed, pass); err != nil || !bytes.Equal(back, plain) {
		return errors.New("the sealed copy did not open back to the original; the file was not changed")
	}

	if err := writeFileAtomic(path, sealed); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	if err := sealRetiredKeysNextTo(path, pass); err != nil {
		return err
	}

	if generated {
		announceBundlePassphrase(stderr, path, pass)
	}

	_, err = fmt.Fprintf(stdout, "Encrypted %s. Keep the passphrase with your other backups.\n", path)

	return err
}

// sealRetiredKeysNextTo seals a plaintext retired-keys.yaml that sits next to the bundle at path, under
// the passphrase the bundle was just sealed with. No such file, or one already encrypted, is left alone.
func sealRetiredKeysNextTo(path, pass string) error {
	retired := retiredFilePath(path)

	plain, err := os.ReadFile(retired)
	if err != nil || isEncryptedBundle(plain) {
		return nil //nolint:nilerr // nothing there to seal
	}

	sealed, err := sealBundleBytes(plain, pass)
	if err != nil {
		return err
	}

	if back, err := openBundleBytes(sealed, pass); err != nil || !bytes.Equal(back, plain) {
		return errors.New("the sealed retired keys did not open back to the original; that file was not changed")
	}

	if err := writeFileAtomic(retired, sealed); err != nil {
		return fmt.Errorf("writing %s: %w", retired, err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(secretsCmd)
	secretsCmd.AddCommand(secretsEncryptCmd)
	secretsEncryptCmd.Flags().StringVar(&secretsEncryptFile, "file", "", "the bundle file to encrypt, instead of a cluster's")
}
