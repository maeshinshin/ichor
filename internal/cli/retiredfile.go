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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// The keys a key rotation retired travel with the exported bundle, in a file of their own next to it: the bundle
// file is the format talosctl owns, and without these secrets a backup sealed before
// the rotation cannot be opened from "bundle + snapshot" alone. The file holds key material, so it is
// stored the way the bundle is: an armored age file under the bundle's passphrase, or plaintext
// exactly when the bundle is.

// retiredFilePath is where the retired keys of the bundle file at bundlePath live.
func retiredFilePath(bundlePath string) string {
	return filepath.Join(filepath.Dir(bundlePath), secretsbundle.RetiredKeysKey)
}

// saveRetiredFile writes retired next to the bundle file at bundlePath (which saveSealedBundle just wrote),
// sealed under the passphrase that file was sealed with, or as plaintext, with the same warning, when the
// bundle is plaintext.
func saveRetiredFile(cluster string, retired *secretsbundle.Retired, bundlePath string, stderr io.Writer) (string, error) {
	dir, err := bundleDir(cluster)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, bundleDirPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	raw, err := retired.Marshal()
	if err != nil {
		return "", errors.New("encoding the retired keys")
	}

	pass := sealedThisRun[bundlePath]

	if bundle, rErr := os.ReadFile(bundlePath); rErr == nil && isEncryptedBundle(bundle) && pass == "" {
		return "", errors.New("the retired keys must be encrypted like the bundle next to them, and its passphrase is not known here")
	}

	if pass != "" {
		if raw, err = sealBundleBytes(raw, pass); err != nil {
			return "", err
		}
	}

	path := filepath.Join(dir, secretsbundle.RetiredKeysKey)
	if err := writeFileAtomic(path, raw); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	if pass != "" {
		sealedThisRun[path] = pass
	} else {
		_, _ = fmt.Fprintf(stderr, "ichor: warning: %s is stored unencrypted (--bundle-plaintext); anyone who can read it holds the retired keys of the cluster\n", path)
	}

	return path, nil
}

// loadRetiredFile reads the retired keys saved next to the bundle file at bundlePath, opening an encrypted
// file with the passphrase that opened (or sealed) the bundle, else from the sources like the bundle's own.
// A bundle with no such file belongs to a cluster that never rotated its keys: that is an empty list, not an error.
func loadRetiredFile(bundlePath string) (*secretsbundle.Retired, error) {
	path := retiredFilePath(bundlePath)

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &secretsbundle.Retired{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the retired keys %q: %w", path, err)
	}

	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
		warnBundle("ichor: warning: %s is accessible to other users (mode %04o); run: chmod 600 %s\n", path, st.Mode().Perm(), path)
	}

	if isEncryptedBundle(raw) {
		pass, ok := openedThisRun[bundlePath]
		if !ok {
			pass, ok = sealedThisRun[bundlePath]
		}

		if !ok {
			if pass, err = readBundlePassphrase(path); err != nil {
				return nil, err
			}
		}

		if raw, err = openBundleBytes(raw, pass); err != nil {
			return nil, fmt.Errorf("opening the retired keys %q: %w", path, err)
		}
	} else {
		warnBundle("ichor: warning: %s is a plaintext file of retired keys; run: ichor secrets encrypt for the cluster's bundle\n", path)
	}

	retired, err := secretsbundle.ParseRetired(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding the retired keys %q: %w", path, err)
	}

	return retired, nil
}
