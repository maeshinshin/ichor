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
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
)

// bundleSecretKey is the key the bundle Secret carries the bundle's bytes
// under (internal/controller/bundle.go's bundleSecretKey, unexported
// there; duplicated here as a literal, not logic, since it is the fixed
// filename convention `talosctl gen secrets` itself uses, not something
// derived by either package).
const bundleSecretKey = "secrets.yaml"

// bundleDirPerm and bundleFilePerm are the permissions
// docs/design/architecture.md's "Trust" section requires for the local
// backup: 0600 on the file, and the containing directory locked down the
// same way (0700) -- a mode-0600 file inside a mode-0755 directory is
// still readable by every other user on the box via the directory's own
// permissions, so the directory has to be tightened too, not just the
// file.
const (
	bundleDirPerm  os.FileMode = 0o700
	bundleFilePerm os.FileMode = 0o600
)

// bundleDir returns ~/.ichor/clusters/<cluster>/, the fixed location
// architecture.md's "Trust" section names for the local secrets bundle
// backup. There is no override flag: the path is part of the recovery
// contract ("ichor restore --recover-cluster rebuilds control planes from
// the local bundle"), and a moving target would undermine that.
func bundleDir(cluster string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory for the secrets bundle backup: %w", err)
	}

	return filepath.Join(home, ".ichor", "clusters", cluster), nil
}

// saveBundleFile writes bundle to ~/.ichor/clusters/<cluster>/secrets.yaml
// with the permissions the Trust section requires, creating the directory
// if necessary. It returns the path written, for the backup nag to name.
func saveBundleFile(cluster string, bundle *secrets.Bundle) (string, error) {
	dir, err := bundleDir(cluster)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, bundleDirPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	// MkdirAll does not chmod a directory that already existed (e.g. from a
	// previous, differently-permissioned run); enforce 0700 explicitly so
	// this is never silently weaker than the Trust section requires.
	if err := os.Chmod(dir, bundleDirPerm); err != nil {
		return "", fmt.Errorf("setting permissions on %s: %w", dir, err)
	}

	out, err := yaml.Marshal(bundle)
	if err != nil {
		return "", fmt.Errorf("encoding secrets bundle: %w", err)
	}

	path := filepath.Join(dir, bundleSecretKey)
	if err := writeFileAtomic(path, out, bundleFilePerm); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	return path, nil
}

// writeFileAtomic writes data to path via a temp file in the same
// directory plus a rename, so a crash mid-write can never leave a
// truncated secrets bundle behind -- the one file in this whole command
// that must never be partially written.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()

	defer func() {
		_ = os.Remove(tmpPath) // no-op once the rename below succeeds
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// backupNagWriter is satisfied by any io.Writer; kept as a named type only
// so nagBackup's signature documents its intent.
type backupNagWriter = io.Writer

// nagBackup prints the backup reminder architecture.md's "Trust" section
// requires ("Generate the secrets bundle locally; nag the user to back it
// up"): losing this file and the Secret both means the cluster can never
// be recovered, so init never proceeds quietly past having just written it.
func nagBackup(w backupNagWriter, path string) {
	_, _ = fmt.Fprintf(w, "\n"+
		"IMPORTANT: the cluster's secrets bundle was written to:\n"+
		"    %s\n"+
		"This is the cluster's only root of trust. Back it up now, somewhere\n"+
		"other than this machine. Without it (and without the Secret this\n"+
		"command is about to create), the cluster cannot be recovered if lost.\n\n",
		path)
}

// loadBundleFile reads the cluster's secrets bundle from a local file,
// encoded the same way `talosctl gen secrets` writes it and the way
// internal/controller/bundle.go reads it back out of a Secret (the key
// "secrets.yaml" that convention describes). adopt predates Ichor's own
// management of the cluster: there is no Secret to read it from
// yet, so it comes from wherever the operator already keeps it, the same
// file `ichor init` would have written to ~/.ichor/clusters/<name>/.
//
// Never wrap a decoding error with the raw bytes or the decoded value --
// only path and structural detail -- so a malformed bundle file can't put
// its own secret material into an error message (pkg/render's leak test
// follows the same rule).
func loadBundleFile(path string) (*secrets.Bundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading secrets bundle %q: %w", path, err)
	}

	var bundle secrets.Bundle
	if err := yaml.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("decoding secrets bundle %q: %w", path, err)
	}

	return &bundle, nil
}
