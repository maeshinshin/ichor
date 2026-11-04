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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adoptRecoverOptions are the --recover-secrets options of the tests below; the bundle is sealed from the
// passphrase sources.
func adoptRecoverOptions() adoptSecretsOptions {
	return adoptSecretsOptions{Recover: true, RecoverFrom: "10.0.0.7:50000", Cluster: "c1", Contract: testVersionContract}
}

// TestResolveAdoptBundle_RecoverEncrypts: a recovered bundle is stored as an age file under the operator's
// passphrase, a rerun that recovers the same bundle needs the passphrase to compare and is accepted, and a
// wrong passphrase is an error of its own, not "already exists" and not an overwrite.
func TestResolveAdoptBundle_RecoverEncrypts(t *testing.T) {
	useBundleCrypto(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(bundlePassphraseEnv, testBundlePass)

	reader := &fakeTalosReader{current: renderRole(t, testControlPlaneRole)}
	opts := adoptRecoverOptions()

	var stderr bytes.Buffer

	bundle, save, err := resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &stderr)
	if err != nil {
		t.Fatal(err)
	}

	if err := save(); err != nil {
		t.Fatal(err)
	}

	dir, _ := bundleDir("c1")
	path := filepath.Join(dir, "secrets.yaml")

	raw, _ := os.ReadFile(path)
	if !isEncryptedBundle(raw) {
		t.Fatal("the recovered bundle was stored in plaintext")
	}

	if strings.Contains(stderr.String(), testBundlePass) || strings.Contains(stderr.String(), bundle.TrustdInfo.Token) {
		t.Fatalf("stderr carries secret material: %q", stderr.String())
	}

	clear(sealedThisRun)

	_, saveAgain, err := resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &stderr)
	if err != nil || saveAgain == nil {
		t.Fatalf("rerun with the identical bundle: %v", err)
	}

	t.Setenv(bundlePassphraseEnv, testBundleWrongPass)

	_, _, err = resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &stderr)
	if !errors.Is(err, errWrongBundlePassphrase) {
		t.Fatalf("wrong passphrase: err = %v", err)
	}

	if after, _ := os.ReadFile(path); !bytes.Equal(raw, after) {
		t.Fatal("the stored bundle changed")
	}
}

// TestResolveAdoptBundle_RecoverRefusesWithoutAPassphrase: the save step refuses, and writes nothing, when
// there is no passphrase and no --bundle-plaintext.
func TestResolveAdoptBundle_RecoverRefusesWithoutAPassphrase(t *testing.T) {
	useBundleCrypto(t)
	t.Setenv("HOME", t.TempDir())

	reader := &fakeTalosReader{current: renderRole(t, testControlPlaneRole)}
	opts := adoptRecoverOptions()

	_, save, err := resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if err := save(); !errors.Is(err, errNoSealPassphrase) {
		t.Fatalf("save err = %v, want errNoSealPassphrase", err)
	}

	if dir, _ := bundleDir("c1"); dirExists(dir) {
		t.Fatal("a refused save wrote something")
	}
}
