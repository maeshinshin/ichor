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
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"golang.org/x/term"

	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// The local secrets bundle is an armored age file with one scrypt passphrase recipient (ADR 0127), so
// `age -d secrets.yaml` opens it without Ichor. Nothing in this file may put a passphrase or bundle
// content into an error or a message.

const (
	// bundlePassphraseEnv is the environment source of a bundle passphrase. The file flag is safer
	// (an environment is readable through /proc by the same user and root).
	bundlePassphraseEnv = "ICHOR_BUNDLE_PASSPHRASE"

	// minBundlePassphrase is the shortest passphrase an operator may seal with; a generated one is longer.
	minBundlePassphrase = 12

	// bundlePlaintextFlag is the opt-out on the commands that write a bundle file.
	bundlePlaintextFlag = "bundle-plaintext"

	ageArmorHeader  = "-----BEGIN AGE ENCRYPTED FILE-----"
	ageBinaryHeader = "age-encryption.org/v1"
)

var (
	errWrongBundlePassphrase = errors.New("wrong passphrase, or the file is damaged")
	errNoBundlePassphrase    = errors.New("no passphrase available")
	// errNoSealPassphraseOnly is the refusal of a command with no plaintext opt-out (secrets encrypt).
	errNoSealPassphraseOnly = errors.New("a new secrets bundle is encrypted, and no passphrase was given: pass " +
		"--bundle-passphrase-file PATH (or /dev/fd/N), set $" + bundlePassphraseEnv + ", or run on a terminal " +
		"to have one generated")
	// errNoSealPassphrase is the same for the commands that have --bundle-plaintext.
	errNoSealPassphrase = fmt.Errorf("%w; --bundle-plaintext stores it unencrypted instead", errNoSealPassphraseOnly)
)

// bundlePassphraseFile is --bundle-passphrase-file.
var bundlePassphraseFile string

// sealedThisRun remembers the passphrase of each file this process sealed, so reading it straight back
// (bundle-export checks what it wrote) does not prompt for a passphrase the operator was just shown.
var sealedThisRun = map[string]string{}

// openedThisRun remembers the passphrase that opened each encrypted bundle this process read: the retired
// keys next to it (ADR 0121) are sealed under the same one, so they must not prompt for it again.
var openedThisRun = map[string]string{}

// readFromFile keeps the passphrase read from --bundle-passphrase-file for the rest of the process: a
// descriptor or a pipe (<(pass show ...)) can be read only once, and a command may need it twice.
var readFromFile = map[string]string{}

// bundleWorkFactor is log2 of scrypt's N when sealing; 0 is age's default (18, about a second). The
// passphrase may be chosen by a person, so the default is kept; a test lowers it.
var bundleWorkFactor int

// bundleTerminal reports whether stdin is a terminal (a person can be prompted); a test replaces it.
var bundleTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) } //nolint:gosec // a file descriptor fits an int

// bundleWarnings receives the warnings about the local files; a test replaces it.
var bundleWarnings io.Writer = os.Stderr

// warnedThisRun holds the warnings already written to bundleWarnings, so a command that reads the
// same file twice (a check, then the work) warns once.
var warnedThisRun = map[string]bool{}

// warnBundle writes a warning about a local file to bundleWarnings, once per distinct message.
func warnBundle(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if warnedThisRun[msg] {
		return
	}

	warnedThisRun[msg] = true

	_, _ = fmt.Fprint(bundleWarnings, msg)
}

// isEncryptedBundle reports whether raw is an age file (armored or binary), by its first line.
func isEncryptedBundle(raw []byte) bool {
	head := strings.TrimSpace(string(raw[:min(len(raw), 128)]))

	return strings.HasPrefix(head, ageArmorHeader) || strings.HasPrefix(head, ageBinaryHeader)
}

// sealBundleBytes encrypts plain to an armored age file under pass.
func sealBundleBytes(plain []byte, pass string) ([]byte, error) {
	rcp, err := age.NewScryptRecipient(pass)
	if err != nil {
		return nil, errors.New("the passphrase was refused by age")
	}

	if bundleWorkFactor != 0 {
		rcp.SetWorkFactor(bundleWorkFactor)
	}

	var out bytes.Buffer

	armored := armor.NewWriter(&out)

	w, err := age.Encrypt(armored, rcp)
	if err != nil {
		return nil, fmt.Errorf("encrypting the secrets bundle: %w", err)
	}

	if _, err := w.Write(plain); err != nil {
		return nil, fmt.Errorf("encrypting the secrets bundle: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("encrypting the secrets bundle: %w", err)
	}

	if err := armored.Close(); err != nil {
		return nil, fmt.Errorf("encrypting the secrets bundle: %w", err)
	}

	return out.Bytes(), nil
}

// openBundleBytes decrypts an age file sealed by sealBundleBytes (or by the age tool). A wrong passphrase
// and a damaged file give the same error, which names neither the passphrase nor any content.
func openBundleBytes(raw []byte, pass string) ([]byte, error) {
	id, err := age.NewScryptIdentity(pass)
	if err != nil {
		return nil, errWrongBundlePassphrase
	}

	src := io.Reader(bytes.NewReader(raw))
	if strings.HasPrefix(strings.TrimSpace(string(raw[:min(len(raw), 128)])), ageArmorHeader) {
		src = armor.NewReader(src)
	}

	r, err := age.Decrypt(src, id)
	if err != nil {
		return nil, errWrongBundlePassphrase
	}

	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, errWrongBundlePassphrase
	}

	return plain, nil
}

// passphraseFromSources returns the passphrase the operator supplied through the file flag or the
// environment, in that order; ok is false when neither is set.
func passphraseFromSources() (pass string, ok bool, err error) {
	if bundlePassphraseFile != "" {
		if pass, ok := readFromFile[bundlePassphraseFile]; ok {
			return pass, true, nil
		}

		raw, err := os.ReadFile(bundlePassphraseFile)
		if err != nil {
			return "", false, fmt.Errorf("reading --bundle-passphrase-file: %w", err)
		}

		if st, err := os.Stat(bundlePassphraseFile); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o077 != 0 {
			warnBundle("ichor: warning: %s is accessible to other users (mode %04o); run: chmod 600 %s\n",
				bundlePassphraseFile, st.Mode().Perm(), bundlePassphraseFile)
		}

		pass = strings.TrimRight(string(raw), "\r\n")
		if pass == "" {
			return "", false, fmt.Errorf("--bundle-passphrase-file %s is empty", bundlePassphraseFile)
		}

		readFromFile[bundlePassphraseFile] = pass

		return pass, true, nil
	}

	if pass = os.Getenv(bundlePassphraseEnv); pass != "" {
		return pass, true, nil
	}

	return "", false, nil
}

// readBundlePassphrase returns the passphrase that opens the encrypted file at path: the file flag, the
// environment, then a no-echo prompt on a terminal.
func readBundlePassphrase(path string) (string, error) {
	if pass, ok := sealedThisRun[path]; ok {
		return pass, nil
	}

	pass, ok, err := passphraseFromSources()
	if err != nil || ok {
		return pass, err
	}

	if !bundleTerminal() {
		return "", fmt.Errorf("%w: %s is encrypted; pass --bundle-passphrase-file PATH (or /dev/fd/N), set $%s, or run on a terminal",
			errNoBundlePassphrase, path, bundlePassphraseEnv)
	}

	pass, err = readPasswordFn("Passphrase for "+path+": ", os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading the passphrase: %w", err)
	}

	if pass == "" {
		return "", errNoBundlePassphrase
	}

	return pass, nil
}

// resolveSealPassphrase chooses how a new bundle file is stored (ADR 0127): "" for plaintext (only when
// asked for), the operator's passphrase from a source, or, on a terminal, a generated one (generated is
// true; the caller shows it once). With no source and no terminal it refuses.
func resolveSealPassphrase(plaintext bool) (pass string, generated bool, err error) {
	src, ok, err := passphraseFromSources()
	if err != nil {
		return "", false, err
	}

	switch {
	case plaintext && ok:
		return "", false, errors.New("--bundle-plaintext and a bundle passphrase (--bundle-passphrase-file or $" + bundlePassphraseEnv + ") exclude each other")
	case plaintext:
		return "", false, nil
	case ok:
		if len(src) < minBundlePassphrase {
			return "", false, fmt.Errorf("the bundle passphrase is shorter than %d characters", minBundlePassphrase)
		}

		return src, false, nil
	case bundleTerminal():
		return generateBundlePassphrase(), true, nil
	}

	return "", false, errNoSealPassphrase
}

// generateBundlePassphrase returns 150 random bits as six groups of five base32 characters.
func generateBundlePassphrase() string {
	t := rand.Text() + rand.Text()[:4] // 26 + 4 base32 characters

	groups := make([]string, 0, 6)
	for i := 0; i < len(t); i += 5 {
		groups = append(groups, t[i:i+5])
	}

	return strings.Join(groups, "-")
}

// saveSealedBundle is how a command that creates or replaces a bundle file stores it (ADR 0127): sealed
// with a passphrase from the sources (or generated on a terminal and shown once on stderr), or plaintext
// when plaintext is true. With reuseExisting, a file already there that is encrypted is replaced under
// the passphrase that opens it, checked first so a typo cannot lock the new copy.
func saveSealedBundle(cluster string, bundle *secrets.Bundle, plaintext, reuseExisting bool, stderr io.Writer) (string, error) {
	dir, err := bundleDir(cluster)
	if err != nil {
		return "", err
	}

	target := filepath.Join(dir, secretsbundle.SecretKey)

	var (
		pass      string
		generated bool
	)

	if existing, rErr := os.ReadFile(target); reuseExisting && rErr == nil && isEncryptedBundle(existing) {
		if pass, err = readBundlePassphrase(target); err == nil {
			_, err = openBundleBytes(existing, pass)
		}
	} else {
		pass, generated, err = resolveSealPassphrase(plaintext)
	}

	if err != nil {
		return "", err
	}

	path, err := saveBundleFile(cluster, bundle, pass)
	if err != nil {
		return "", err
	}

	if pass != "" {
		sealedThisRun[path] = pass
	}

	switch {
	case generated:
		announceBundlePassphrase(stderr, path, pass)
	case pass == "":
		_, _ = fmt.Fprintf(stderr, "ichor: warning: %s is stored unencrypted (--bundle-plaintext); anyone who can read it holds every CA key of the cluster\n", path)
	}

	return path, nil
}

// announceBundlePassphrase shows a generated passphrase, once, on w.
func announceBundlePassphrase(w io.Writer, path, pass string) {
	_, _ = fmt.Fprintf(w, "\n"+
		"The secrets bundle is encrypted with this passphrase (shown once, stored nowhere):\n\n"+
		"    %s\n\n"+
		"Save it now, in a password manager or somewhere else off this machine. Without it %s cannot be\n"+
		"opened (it is a standard age file: `age -d` opens it).\n\n", pass, path)
}
