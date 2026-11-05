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
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestBundleFileEncryptedRoundTrip: a file sealed with a passphrase is an opaque age file at 0600 and loads
// back with the passphrase from a source.
func TestBundleFileEncryptedRoundTrip(t *testing.T) {
	useBundleCrypto(t)

	bundle := newTestBundle(t)

	path := saveTestBundle(t, "enc", bundle, testBundlePass)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !isEncryptedBundle(raw) || bytes.Contains(raw, []byte(bundle.Cluster.ID)) {
		t.Fatal("the file is not encrypted")
	}

	if runtime.GOOS != osWindows {
		if st, _ := os.Stat(path); st.Mode().Perm() != bundleFilePerm {
			t.Fatalf("mode = %o", st.Mode().Perm())
		}
	}

	t.Setenv(bundlePassphraseEnv, testBundlePass)

	got, err := loadBundleFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.Cluster.ID != bundle.Cluster.ID || got.Secrets.BootstrapToken != bundle.Secrets.BootstrapToken {
		t.Fatal("the opened bundle differs")
	}
}

func TestLoadBundleFileEncryptedFailures(t *testing.T) {
	warn := useBundleCrypto(t)

	path := saveTestBundle(t, "enc", newTestBundle(t), testBundlePass)

	t.Run("wrong passphrase", func(t *testing.T) {
		t.Setenv(bundlePassphraseEnv, testBundleWrongPass)

		_, err := loadBundleFile(path)
		if !errors.Is(err, errWrongBundlePassphrase) {
			t.Fatalf("err = %v", err)
		}

		if strings.Contains(err.Error(), testBundleWrongPass) || strings.Contains(err.Error(), testBundlePass) {
			t.Fatalf("error names a passphrase: %v", err)
		}
	})

	t.Run("no passphrase", func(t *testing.T) {
		_, err := loadBundleFile(path)
		if !errors.Is(err, errNoBundlePassphrase) || !strings.Contains(err.Error(), path) {
			t.Fatalf("err = %v", err)
		}
	})

	if warn.Len() != 0 {
		t.Fatalf("an encrypted bundle drew a warning: %q", warn.String())
	}
}

// TestLoadBundleFilePlaintextWarns: an existing plaintext bundle keeps working, loudly, and the warning
// names the migration.
func TestLoadBundleFilePlaintextWarns(t *testing.T) {
	warn := useBundleCrypto(t)

	bundle := newTestBundle(t)

	path := saveTestBundle(t, "plain", bundle, "")

	got, err := loadBundleFile(path)
	if err != nil || got.Cluster.ID != bundle.Cluster.ID {
		t.Fatalf("plaintext bundle did not load: %v", err)
	}

	for _, want := range []string{"plaintext", "ichor secrets encrypt --file " + path} {
		if !strings.Contains(warn.String(), want) {
			t.Errorf("warning %q lacks %q", warn.String(), want)
		}
	}

	if strings.Contains(warn.String(), bundle.Secrets.BootstrapToken) {
		t.Fatal("the warning contains bundle material")
	}
}

// TestSaveSealedBundle covers the choices of the commands that write a bundle file.
func TestSaveSealedBundle(t *testing.T) {
	t.Run("refuses without a passphrase and writes nothing", func(t *testing.T) {
		useBundleCrypto(t)

		home := t.TempDir()
		t.Setenv("HOME", home)

		_, err := saveSealedBundle("c", newTestBundle(t), false, false, &bytes.Buffer{})
		if !errors.Is(err, errNoSealPassphrase) {
			t.Fatalf("err = %v", err)
		}

		if dir, _ := bundleDir("c"); dirExists(dir) {
			t.Fatalf("a refused save created %s", dir)
		}
	})

	t.Run("plaintext opt-out warns", func(t *testing.T) {
		useBundleCrypto(t)

		var errw bytes.Buffer

		path, err := saveSealedBundle("c", newTestBundle(t), true, false, &errw)
		if err != nil {
			t.Fatal(err)
		}

		if raw, _ := os.ReadFile(path); isEncryptedBundle(raw) {
			t.Fatal("--bundle-plaintext wrote an encrypted file")
		}

		if !strings.Contains(errw.String(), "unencrypted") {
			t.Fatalf("no warning: %q", errw.String())
		}
	})

	t.Run("a generated passphrase is shown once and opens the file", func(t *testing.T) {
		useBundleCrypto(t)

		bundleTerminal = func() bool { return true }

		var errw bytes.Buffer

		path, err := saveSealedBundle("c", newTestBundle(t), false, false, &errw)
		if err != nil {
			t.Fatal(err)
		}

		pass := generatedFrom(t, errw.String())
		if strings.Count(errw.String(), pass) != 1 {
			t.Fatalf("the passphrase is not shown exactly once: %q", errw.String())
		}

		raw, _ := os.ReadFile(path)
		if _, err := openBundleBytes(raw, pass); err != nil {
			t.Fatalf("the shown passphrase does not open the file: %v", err)
		}
	})

	t.Run("a passphrase from a source is never printed", func(t *testing.T) {
		useBundleCrypto(t)
		t.Setenv(bundlePassphraseEnv, testBundlePass)

		var errw bytes.Buffer

		if _, err := saveSealedBundle("c", newTestBundle(t), false, false, &errw); err != nil {
			t.Fatal(err)
		}

		if strings.Contains(errw.String(), testBundlePass) {
			t.Fatalf("the passphrase was printed: %q", errw.String())
		}
	})

	t.Run("an existing encrypted file keeps its passphrase, and a wrong one changes nothing", func(t *testing.T) {
		useBundleCrypto(t)

		path := saveTestBundle(t, "c", newTestBundle(t), testBundlePass)

		before, _ := os.ReadFile(path)

		t.Setenv(bundlePassphraseEnv, testBundleWrongPass)

		if _, err := saveSealedBundle("c", newFreshTestBundle(t), false, true, &bytes.Buffer{}); !errors.Is(err, errWrongBundlePassphrase) {
			t.Fatalf("err = %v, want errWrongBundlePassphrase", err)
		}

		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Fatal("a wrong passphrase replaced the file")
		}

		t.Setenv(bundlePassphraseEnv, testBundlePass)
		clear(sealedThisRun)

		fresh := newFreshTestBundle(t)
		if _, err := saveSealedBundle("c", fresh, false, true, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}

		got, err := loadBundleFile(path)
		if err != nil || got.Cluster.ID != fresh.Cluster.ID {
			t.Fatalf("the replaced file does not hold the new bundle: %v", err)
		}
	})
}

func dirExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// generatedFrom extracts the passphrase announceBundlePassphrase printed.
func generatedFrom(t *testing.T, out string) string {
	t.Helper()

	for l := range strings.SplitSeq(out, "\n") {
		if p, ok := strings.CutPrefix(l, "    "); ok && strings.Count(p, "-") == 5 {
			return p
		}
	}

	t.Fatalf("no generated passphrase in %q", out)

	return ""
}
