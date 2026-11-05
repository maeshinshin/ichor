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
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

const (
	testBundlePass      = "correct horse battery staple"
	testBundleWrongPass = "tr0ub4dor&3 and more words"
)

// useBundleCrypto makes sealing cheap and the passphrase sources empty and non-interactive, the state
// of a script: a test then opts in to the source it exercises. It returns the buffer that collects the
// warnings loadBundleFile and the savers write.
func useBundleCrypto(t *testing.T) *bytes.Buffer {
	t.Helper()

	oldFactor, oldFile, oldTerm, oldPrompt, oldWarn := bundleWorkFactor, bundlePassphraseFile, bundleTerminal, readPasswordFn, bundleWarnings

	var warn bytes.Buffer

	bundleWorkFactor, bundlePassphraseFile, bundleWarnings = 10, "", &warn
	bundleTerminal = func() bool { return false }

	t.Setenv(bundlePassphraseEnv, "")
	clear(sealedThisRun)
	clear(openedThisRun)
	clear(readFromFile)
	clear(warnedThisRun)
	t.Cleanup(func() {
		clear(sealedThisRun)
		clear(openedThisRun)
		clear(readFromFile)
		clear(warnedThisRun)
		bundleWorkFactor, bundlePassphraseFile, bundleTerminal, readPasswordFn, bundleWarnings = oldFactor, oldFile, oldTerm, oldPrompt, oldWarn
	})

	return &warn
}

// savePlainBundle is saveBundleFile without encryption, for tests that fake the save seam.
func savePlainBundle(cluster string, bundle *secrets.Bundle) (string, error) {
	return saveBundleFile(cluster, bundle, "")
}

func writePassphraseFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestSealOpenBundleRoundTrip(t *testing.T) {
	useBundleCrypto(t)

	plain := []byte("cluster:\n  id: abc\n")

	sealed, err := sealBundleBytes(plain, testBundlePass)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(sealed, []byte("cluster:")) || !isEncryptedBundle(sealed) {
		t.Fatalf("sealed bytes are not an opaque age file: %q", sealed)
	}

	if !strings.HasPrefix(string(sealed), "-----BEGIN AGE ENCRYPTED FILE-----") {
		t.Fatalf("sealed file is not armored: %q", sealed[:40])
	}

	got, err := openBundleBytes(sealed, testBundlePass)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, plain) {
		t.Fatalf("opened = %q, want %q", got, plain)
	}
}

func TestOpenBundleWrongPassphraseAndDamage(t *testing.T) {
	useBundleCrypto(t)

	sealed, err := sealBundleBytes([]byte("secret: value\n"), testBundlePass)
	if err != nil {
		t.Fatal(err)
	}

	_, err = openBundleBytes(sealed, testBundleWrongPass)
	if !errors.Is(err, errWrongBundlePassphrase) {
		t.Fatalf("wrong passphrase: err = %v, want errWrongBundlePassphrase", err)
	}

	for _, s := range []string{testBundlePass, testBundleWrongPass} {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("error names a passphrase: %v", err)
		}
	}

	damaged := bytes.Replace(sealed, []byte("\n"), []byte("\nAAAA"), 2)
	if _, err := openBundleBytes(damaged, testBundlePass); err == nil {
		t.Fatal("a damaged file opened")
	}
}

func TestIsEncryptedBundle(t *testing.T) {
	cases := map[string]bool{
		"-----BEGIN AGE ENCRYPTED FILE-----\nxx":      true,
		"\n  -----BEGIN AGE ENCRYPTED FILE-----\nxx":  true,
		"age-encryption.org/v1\n-> scrypt":            true,
		"cluster:\n  id: x\n":                         false,
		"":                                            false,
		"# age-encryption.org/v1 mentioned in YAML\n": false,
	}
	for in, want := range cases {
		if got := isEncryptedBundle([]byte(in)); got != want {
			t.Errorf("isEncryptedBundle(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestReadBundlePassphraseSources(t *testing.T) {
	t.Run("file wins over the environment, a trailing newline is dropped", func(t *testing.T) {
		useBundleCrypto(t)
		t.Setenv(bundlePassphraseEnv, "from-the-environment-0")

		bundlePassphraseFile = writePassphraseFile(t, testBundlePass+"\r\n")

		got, err := readBundlePassphrase("x/secrets.yaml")
		if err != nil || got != testBundlePass {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("environment", func(t *testing.T) {
		useBundleCrypto(t)
		t.Setenv(bundlePassphraseEnv, testBundlePass)

		got, err := readBundlePassphrase("x")
		if err != nil || got != testBundlePass {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("an empty file is an error", func(t *testing.T) {
		useBundleCrypto(t)

		bundlePassphraseFile = writePassphraseFile(t, "\n")

		if _, err := readBundlePassphrase("x"); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("a missing file is an error that names the path, not content", func(t *testing.T) {
		useBundleCrypto(t)

		bundlePassphraseFile = filepath.Join(t.TempDir(), "absent")

		if _, err := readBundlePassphrase("x"); err == nil || !strings.Contains(err.Error(), "absent") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("no source and no terminal names the three ways", func(t *testing.T) {
		useBundleCrypto(t)

		_, err := readBundlePassphrase("x/secrets.yaml")
		if err == nil {
			t.Fatal("expected an error")
		}

		for _, want := range []string{"--bundle-passphrase-file", bundlePassphraseEnv, "x/secrets.yaml"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})

	t.Run("a terminal is prompted", func(t *testing.T) {
		useBundleCrypto(t)

		bundleTerminal = func() bool { return true }
		readPasswordFn = func(prompt string, _ io.Writer) (string, error) {
			if !strings.Contains(prompt, "x/secrets.yaml") {
				t.Errorf("prompt %q does not name the file", prompt)
			}

			return testBundlePass, nil
		}

		got, err := readBundlePassphrase("x/secrets.yaml")
		if err != nil || got != testBundlePass {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}

func TestResolveSealPassphrase(t *testing.T) {
	t.Run("plaintext opt-out", func(t *testing.T) {
		useBundleCrypto(t)

		pass, generated, err := resolveSealPassphrase(true)
		if err != nil || pass != "" || generated {
			t.Fatalf("got %q, %v, %v", pass, generated, err)
		}
	})

	t.Run("plaintext and a passphrase source exclude each other", func(t *testing.T) {
		useBundleCrypto(t)
		t.Setenv(bundlePassphraseEnv, testBundlePass)

		if _, _, err := resolveSealPassphrase(true); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("a source is used as given", func(t *testing.T) {
		useBundleCrypto(t)

		bundlePassphraseFile = writePassphraseFile(t, testBundlePass)

		pass, generated, err := resolveSealPassphrase(false)
		if err != nil || pass != testBundlePass || generated {
			t.Fatalf("got %q, %v, %v", pass, generated, err)
		}
	})

	t.Run("a short passphrase is refused", func(t *testing.T) {
		useBundleCrypto(t)
		t.Setenv(bundlePassphraseEnv, "short")

		_, _, err := resolveSealPassphrase(false)
		if err == nil || strings.Contains(err.Error(), "short\"") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("no source and no terminal fails closed", func(t *testing.T) {
		useBundleCrypto(t)

		_, _, err := resolveSealPassphrase(false)
		if !errors.Is(err, errNoSealPassphrase) {
			t.Fatalf("err = %v, want errNoSealPassphrase", err)
		}

		if !strings.Contains(err.Error(), "--bundle-plaintext") {
			t.Errorf("error does not name the opt-out: %v", err)
		}
	})

	t.Run("a terminal gets a generated passphrase", func(t *testing.T) {
		useBundleCrypto(t)

		bundleTerminal = func() bool { return true }

		a, generated, err := resolveSealPassphrase(false)
		if err != nil || !generated {
			t.Fatalf("got %q, %v, %v", a, generated, err)
		}

		if !regexp.MustCompile(`^([A-Z2-7]{5}-){5}[A-Z2-7]{5}$`).MatchString(a) {
			t.Fatalf("generated passphrase %q is not six groups of five", a)
		}

		b, _, _ := resolveSealPassphrase(false)
		if a == b {
			t.Fatal("two generated passphrases are equal")
		}
	})
}
