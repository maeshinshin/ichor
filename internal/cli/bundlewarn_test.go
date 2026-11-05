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
	"strings"
	"testing"
)

// `ichor secrets encrypt` has no --bundle-plaintext flag, so its refusal must not offer it.
func TestSecretsEncryptRefusalDoesNotOfferBundlePlaintext(t *testing.T) {
	useBundleCrypto(t)
	t.Setenv("HOME", t.TempDir())

	path, err := saveBundleFile("plain", newTestBundle(t), "")
	if err != nil {
		t.Fatal(err)
	}

	err = runSecretsEncrypt(path, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("encrypt succeeded without a passphrase")
	}

	if strings.Contains(err.Error(), "--bundle-plaintext") || !strings.Contains(err.Error(), "--bundle-passphrase-file") {
		t.Fatalf("unexpected refusal: %v", err)
	}

	// init, adopt and bundle-export do have the flag, and keep offering it.
	if !strings.Contains(errNoSealPassphrase.Error(), "--bundle-plaintext") {
		t.Fatalf("the refusal of the commands that have --bundle-plaintext lost it: %v", errNoSealPassphrase)
	}
}

// A command that reads the same plaintext bundle twice (cluster credentials --bundle checks it, then
// writes from it) warns once.
func TestLoadBundleFilePlaintextWarnsOnce(t *testing.T) {
	warn := useBundleCrypto(t)
	t.Setenv("HOME", t.TempDir())

	path, err := saveBundleFile("plain", newTestBundle(t), "")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, err := loadBundleFile(path); err != nil {
			t.Fatal(err)
		}
	}

	if n := strings.Count(warn.String(), "is a plaintext secrets bundle"); n != 1 {
		t.Fatalf("the plaintext warning was printed %d times:\n%s", n, warn.String())
	}
}
