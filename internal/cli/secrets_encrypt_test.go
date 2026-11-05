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
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSecretsEncryptMigratesAPlaintextBundle(t *testing.T) {
	useBundleCrypto(t)
	t.Setenv(bundlePassphraseEnv, testBundlePass)

	bundle := newTestBundle(t)

	path := saveTestBundle(t, "old", bundle, "")

	var stdout, stderr bytes.Buffer

	if err := runSecretsEncrypt(path, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	if !isEncryptedBundle(raw) {
		t.Fatal("the file was not encrypted")
	}

	if runtime.GOOS != osWindows {
		if st, _ := os.Stat(path); st.Mode().Perm() != bundleFilePerm {
			t.Fatalf("mode = %o", st.Mode().Perm())
		}
	}

	got, err := loadBundleFile(path)
	if err != nil || got.Cluster.ID != bundle.Cluster.ID || got.Secrets.BootstrapToken != bundle.Secrets.BootstrapToken {
		t.Fatalf("the migrated file does not hold the same bundle: %v", err)
	}

	if out := stdout.String() + stderr.String(); strings.Contains(out, testBundlePass) || strings.Contains(out, bundle.Secrets.BootstrapToken) {
		t.Fatalf("output contains secret material: %q", out)
	}

	// Running it again is a no-op that needs no passphrase.
	t.Setenv(bundlePassphraseEnv, "")

	stdout.Reset()

	if err := runSecretsEncrypt(path, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "already encrypted") {
		t.Fatalf("second run: %v %q", err, stdout.String())
	}

	if again, _ := os.ReadFile(path); !bytes.Equal(raw, again) {
		t.Fatal("an already encrypted file was rewritten")
	}
}

func TestSecretsEncryptRefusesAndLeavesTheFile(t *testing.T) {
	useBundleCrypto(t)

	t.Run("no passphrase source", func(t *testing.T) {
		path := saveTestBundle(t, "old", newTestBundle(t), "")

		before, _ := os.ReadFile(path)

		if err := runSecretsEncrypt(path, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected a refusal")
		}

		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Fatal("the file changed")
		}
	})

	t.Run("not a bundle", func(t *testing.T) {
		t.Setenv(bundlePassphraseEnv, testBundlePass)

		path := filepath.Join(t.TempDir(), "other.yaml")
		if err := os.WriteFile(path, []byte("hello: world\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := runSecretsEncrypt(path, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("expected a refusal")
		}

		if raw, _ := os.ReadFile(path); string(raw) != "hello: world\n" {
			t.Fatal("the file changed")
		}
	})
}

func TestSecretsEncryptOnATerminalGeneratesAndShowsOnce(t *testing.T) {
	useBundleCrypto(t)

	bundleTerminal = func() bool { return true }

	path := saveTestBundle(t, "old", newTestBundle(t), "")

	var stdout, stderr bytes.Buffer

	if err := runSecretsEncrypt(path, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	pass := generatedFrom(t, stderr.String())
	if strings.Count(stderr.String()+stdout.String(), pass) != 1 {
		t.Fatal("the generated passphrase is not shown exactly once")
	}

	raw, _ := os.ReadFile(path)
	if _, err := openBundleBytes(raw, pass); err != nil {
		t.Fatalf("the shown passphrase does not open the file: %v", err)
	}
}

func TestSecretsEncryptCommandArguments(t *testing.T) {
	useBundleCrypto(t)
	t.Setenv(bundlePassphraseEnv, testBundlePass)

	path := saveTestBundle(t, "named", newTestBundle(t), "")

	old := secretsEncryptFile

	t.Cleanup(func() { secretsEncryptFile = old })

	secretsEncryptFile = ""

	secretsEncryptCmd.SetOut(&bytes.Buffer{})
	secretsEncryptCmd.SetErr(&bytes.Buffer{})

	if err := secretsEncryptCmd.RunE(secretsEncryptCmd, nil); err == nil {
		t.Fatal("no cluster and no --file: expected an error")
	}

	if err := secretsEncryptCmd.RunE(secretsEncryptCmd, []string{"named"}); err != nil {
		t.Fatal(err)
	}

	if raw, _ := os.ReadFile(path); !isEncryptedBundle(raw) {
		t.Fatal("the named cluster's bundle was not encrypted")
	}
}

// TestInitBundleProtection: init encrypts by default, refuses before doing anything when it has no way to,
// and shows a generated passphrase once.
func TestInitBundleProtection(t *testing.T) {
	run := func(t *testing.T, mutate func(*initOptions)) (string, string, error) {
		t.Helper()
		useBundleCrypto(t)
		withTempHome(t)

		k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
		deps, _, _ := newFakeInitDeps(t, k8s)

		var stdout, stderr bytes.Buffer

		deps.Stdout, deps.Stderr = &stdout, &stderr

		opts := validInitOptions()
		opts.BundlePlaintext = false
		mutate(&opts)

		err := runInit(context.Background(), opts, deps)

		return stdout.String(), stderr.String(), err
	}

	t.Run("a passphrase seals the file and is never printed", func(t *testing.T) {
		stdout, stderr, err := run(t, func(o *initOptions) { o.BundlePassphrase = testBundlePass })
		if err != nil {
			t.Fatal(err)
		}

		dir, _ := bundleDir(testInitClusterName)

		raw, err := os.ReadFile(filepath.Join(dir, "secrets.yaml"))
		if err != nil || !isEncryptedBundle(raw) {
			t.Fatalf("secrets.yaml is not encrypted: %v", err)
		}

		if strings.Contains(stdout+stderr, testBundlePass) {
			t.Fatal("the passphrase was printed")
		}

		// Nor does anything the run printed carry the bundle's own material.
		t.Setenv(bundlePassphraseEnv, testBundlePass)

		saved, err := loadBundleFile(filepath.Join(dir, "secrets.yaml"))
		if err != nil {
			t.Fatal(err)
		}

		for _, s := range bundleSecretMaterial(saved) {
			if s != "" && strings.Contains(stdout+stderr, s) {
				t.Fatal("init output contains bundle secret material")
			}
		}

		if _, err := openBundleBytes(raw, testBundlePass); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("neither a passphrase nor the opt-out is refused before any step", func(t *testing.T) {
		_, stderr, err := run(t, func(*initOptions) {})
		if err == nil || !strings.Contains(err.Error(), "--bundle-plaintext") {
			t.Fatalf("err = %v", err)
		}

		if strings.Contains(stderr, "Secrets bundle") {
			t.Fatalf("a step ran before the refusal: %q", stderr)
		}

		if dir, _ := bundleDir(testInitClusterName); dirExists(dir) {
			t.Fatal("a refused init created the bundle directory")
		}
	})
}

func TestChooseInitBundleProtection(t *testing.T) {
	t.Run("a terminal gets a generated passphrase shown once, then waits for Enter", func(t *testing.T) {
		useBundleCrypto(t)

		bundleTerminal = func() bool { return true }

		var errw bytes.Buffer

		cmd := initCmd
		cmd.SetErr(&errw)
		cmd.SetIn(strings.NewReader("\n"))

		opts := initOptions{Cluster: "demo"}

		if err := chooseInitBundleProtection(cmd, &opts); err != nil {
			t.Fatal(err)
		}

		if opts.BundlePassphrase == "" || strings.Count(errw.String(), opts.BundlePassphrase) != 1 {
			t.Fatalf("passphrase %q not shown exactly once in %q", opts.BundlePassphrase, errw.String())
		}

		if !strings.Contains(errw.String(), "Press Enter") {
			t.Fatal("init did not wait for the operator to save the passphrase")
		}
	})

	t.Run("a source is used and not printed", func(t *testing.T) {
		useBundleCrypto(t)
		t.Setenv(bundlePassphraseEnv, testBundlePass)

		var errw bytes.Buffer

		cmd := initCmd
		cmd.SetErr(&errw)

		opts := initOptions{}

		if err := chooseInitBundleProtection(cmd, &opts); err != nil || opts.BundlePassphrase != testBundlePass {
			t.Fatalf("got %q, %v", opts.BundlePassphrase, err)
		}

		if errw.Len() != 0 {
			t.Fatalf("printed %q", errw.String())
		}
	})

	t.Run("plaintext", func(t *testing.T) {
		useBundleCrypto(t)

		opts := initOptions{BundlePlaintext: true}
		if err := chooseInitBundleProtection(initCmd, &opts); err != nil || opts.BundlePassphrase != "" {
			t.Fatalf("got %q, %v", opts.BundlePassphrase, err)
		}
	})

	t.Run("nothing to go on", func(t *testing.T) {
		useBundleCrypto(t)

		if err := chooseInitBundleProtection(initCmd, &initOptions{}); err == nil {
			t.Fatal("expected a refusal")
		}
	})
}
