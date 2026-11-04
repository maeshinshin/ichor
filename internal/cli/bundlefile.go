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
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

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
// if necessary. A non-empty pass seals it as an age file (ADR 0127); ""
// writes plaintext, which only a caller that was asked for it may do. It
// returns the path written, for the backup nag to name.
func saveBundleFile(cluster string, bundle *secrets.Bundle, pass string) (string, error) {
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

	if pass != "" {
		if out, err = sealBundleBytes(out, pass); err != nil {
			return "", err
		}
	}

	path := filepath.Join(dir, secretsbundle.SecretKey)
	if err := writeFileAtomic(path, out); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	return path, nil
}

// kubeconfigFilename is the file saveKubeconfigFile writes next to the
// secrets bundle, in the same ~/.ichor/clusters/<cluster>/ directory --
// the same convention talosctl itself follows after a bootstrap.
const kubeconfigFilename = "kubeconfig"

// saveKubeconfigFile writes the kubeconfig design step 4 fetches
// ("Bootstrap etcd, fetch the kubeconfig, store the bundle as a Secret")
// to ~/.ichor/clusters/<cluster>/kubeconfig, at the same 0600 the bundle
// gets: it carries a client certificate and key, not a Talos secret, but
// still a credential this command should not leave world-readable.
func saveKubeconfigFile(cluster string, kubeconfig []byte) (string, error) {
	dir, err := bundleDir(cluster)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, bundleDirPerm); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	// MkdirAll leaves an existing directory's mode alone; see saveBundleFile.
	if err := os.Chmod(dir, bundleDirPerm); err != nil {
		return "", fmt.Errorf("setting permissions on %s: %w", dir, err)
	}

	path := filepath.Join(dir, kubeconfigFilename)
	if err := writeFileAtomic(path, kubeconfig); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	return path, nil
}

// buildBundleSecret builds the Secret design step 4 stores the bundle in
// ("store the bundle as a Secret"), named after
// cluster.Spec.SecretsRef.Name and keyed the same way the local file is
// (secretsbundle.SecretKey), so a recovery reading either back gets identical
// bytes. It lives in cluster.Namespace: ADR 0011 resolves the bundle
// Secret in the controller's own namespace rather than the TalosCluster's,
// and the default self-managed deployment (one controller, one
// namespace, architecture.md's "Deployment") makes those the same
// namespace for a cluster `ichor init` creates -- the --namespace flag
// this command takes is that one namespace, used for both.
func buildBundleSecret(cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle) (*corev1.Secret, error) {
	raw, err := yaml.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("encoding secrets bundle: %w", err)
	}

	return &corev1.Secret{
		Name: cluster.Spec.SecretsRef.Name, Namespace: cluster.Namespace,
		Data: map[string][]byte{secretsbundle.SecretKey: raw},
	}, nil
}

// fsyncFile lets a test observe that the fsync call site below is
// actually reached on the happy path, without needing to reproduce a
// crash: the default is just f.Sync(); a test swaps it for a spy and
// restores it afterward.
var fsyncFile = func(f *os.File) error { return f.Sync() }

// writeFileAtomic writes data to path, at bundleFilePerm (0600 -- every
// caller is the secrets bundle or the kubeconfig next to it, nothing
// else needs this function), via a temp file in the same directory,
// fsyncs both the temp file's data and (after the rename) the directory
// entry, so a crash mid-write can never leave a truncated secrets bundle
// behind -- the one file in this whole command that must never be
// partially written.
//
// What this guards against: a process kill (SIGKILL/power loss/kernel
// panic) at any point up to and including right after this function
// returns leaves either the old content at path (if it existed) or
// nothing -- never a truncated or garbage file. Without the two fsyncs
// below, a kill between the rename and its directory entry reaching disk
// is the classic failure this exists to close: the data blocks never hit
// disk, but the rename looked complete, so the write silently loses the
// file it just claimed to have written -- worse than no backup at all,
// since the operator believes one exists. Rename itself is atomic (POSIX,
// same filesystem, same directory) so there is no window where path
// exists but is half-written; what the fsyncs add is that the *new*
// content (not stale content, not nothing) is what survives power loss.
// What this does not guard against: a filesystem that lies about fsync
// (some network filesystems, some misconfigured block devices, a disk
// with write caching enabled and no battery/capacitor backing) -- there
// is no portable way to detect that from userspace, so this function
// trusts the kernel's fsync contract the same way every other program
// that cares about durability does.
func writeFileAtomic(path string, data []byte) error {
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

	// Sync before Close: the data has to reach disk while this function
	// still holds the file open, not left to whenever the OS feels like
	// flushing a page cache it has already forgotten was ours to track.
	if err := fsyncFile(tmp); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("syncing %s: %w", tmpPath, err)
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmpPath, bundleFilePerm); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	return fsyncDir(dir)
}

// fsyncDir fsyncs dir itself, so the directory entry the preceding rename
// just created or replaced is durable too -- a renamed-but-not-yet-synced
// directory entry is exactly the kind of loss a crash right after
// writeFileAtomic returns could otherwise cause.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s to sync it: %w", dir, err)
	}

	defer func() { _ = d.Close() }()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("syncing directory %s: %w", dir, err)
	}

	return nil
}

// backupNagWriter is satisfied by any io.Writer; kept as a named type only
// so nagBackup's signature documents its intent.
type backupNagWriter = io.Writer

// nagBackup prints the backup reminder architecture.md's "Trust" section
// requires ("Generate the secrets bundle locally; nag the user to back it
// up"): losing this file and the Secret both means the cluster can never
// be recovered (ADR, "Full-loss recovery": "bundle + snapshot is
// sufficient"), so init never proceeds quietly past having just written it.
func nagBackup(w backupNagWriter, path string) {
	_, _ = fmt.Fprintf(w, "\n"+
		"IMPORTANT: the cluster's secrets bundle was written to:\n"+
		"    %s\n"+
		"This is the cluster's only root of trust. Back it up now, somewhere\n"+
		"other than this machine. Without it (and without the Secret this\n"+
		"command is about to create), the cluster cannot be recovered if lost.\n\n",
		path)
}

// readBundlePlain reads the bundle file at path and returns its YAML, decrypting an age file with the
// passphrase from the sources (ADR 0127). encrypted says which it was. Errors name the path only.
func readBundlePlain(path string) (plain []byte, encrypted bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading secrets bundle %q: %w", path, err)
	}

	if !isEncryptedBundle(raw) {
		return raw, false, nil
	}

	pass, err := readBundlePassphrase(path)
	if err != nil {
		return nil, true, err
	}

	if plain, err = openBundleBytes(raw, pass); err != nil {
		return nil, true, fmt.Errorf("opening secrets bundle %q: %w", path, err)
	}

	return plain, true, nil
}

// loadBundleFile reads the cluster's secrets bundle from a local file,
// encoded the same way `talosctl gen secrets` writes it and the way
// internal/controller/bundle.go reads it back out of a Secret (the key
// "secrets.yaml" that convention describes), or sealed as an age file
// (ADR 0127). adopt predates Ichor's own
// management of the cluster (ADR 0014): there is no Secret to read it from
// yet, so it comes from wherever the operator already keeps it, the same
// file `ichor init` would have written to ~/.ichor/clusters/<name>/.
//
// A plaintext file still loads, with a warning that names the migration.
//
// Never wrap a decoding error with the raw bytes or the decoded value --
// only path and structural detail -- so a malformed bundle file can't put
// its own secret material into an error message (pkg/render's leak test
// follows the same rule).
func loadBundleFile(path string) (*secrets.Bundle, error) {
	raw, encrypted, err := readBundlePlain(path)
	if err != nil {
		return nil, err
	}

	if !encrypted {
		_, _ = fmt.Fprintf(bundleWarnings, "ichor: warning: %s is a plaintext secrets bundle (every CA key of the cluster); run: ichor secrets encrypt --file %s\n", path, path)
	}

	// The mode of the file matters most for plaintext, but a loose mode is worth a word for any bundle.
	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
		_, _ = fmt.Fprintf(bundleWarnings, "ichor: warning: %s is accessible to other users (mode %04o); run: chmod 600 %s\n", path, st.Mode().Perm(), path)
	}

	bundle, err := secretsbundle.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding secrets bundle %q: %w", path, err)
	}

	return bundle, nil
}
