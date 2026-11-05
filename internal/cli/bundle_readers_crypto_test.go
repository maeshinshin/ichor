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
	"strings"
	"testing"
	"time"
)

// The readers of the local bundle all go through loadBundleFile; these tests pin that restore and
// the offline credentials reach an encrypted file through the same passphrase sources.

func TestRestoreRecoverClusterFromAnEncryptedBundle(t *testing.T) {
	useBundleCrypto(t)

	h := newRestoreHarness(t) // saved the bundle as plaintext; replace it with a sealed one
	saveTestBundle(t, "lost", h.bundle, testBundlePass)

	snap := goodSnapshot(t)
	putSnapshot(t, h.store, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), snap, h.bundle)

	t.Run("without the passphrase it stops before touching the node", func(t *testing.T) {
		err := h.run()
		if !errors.Is(err, errNoBundlePassphrase) {
			t.Fatalf("err = %v", err)
		}

		if len(h.talos.order) != 0 {
			t.Fatalf("the node was called: %v", h.talos.order)
		}
	})

	t.Run("with the passphrase file", func(t *testing.T) {
		bundlePassphraseFile = writePassphraseFile(t, testBundlePass+"\n")

		err := h.run()
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(h.talos.uploaded, snap) {
			t.Fatal("Talos did not receive the snapshot")
		}

		h.assertNoLeak(t, err)

		if strings.Contains(h.stdout.String()+h.stderr.String(), testBundlePass) {
			t.Fatal("the passphrase was printed")
		}
	})
}

func TestCredentialsFromAnEncryptedBundleFileWorkOffline(t *testing.T) {
	useBundleCrypto(t)

	f := newCredFixture(t, false, "")

	path := saveTestBundle(t, "offline", f.old, testBundlePass)

	req := f.request()
	req.BundleFile = path
	req.TalosEndpoints = []string{testOtherAddr}

	if _, err := writeCredentials(context.Background(), nil, req); !errors.Is(err, errNoBundlePassphrase) {
		t.Fatalf("without the passphrase: err = %v", err)
	}

	t.Setenv(bundlePassphraseEnv, testBundlePass)

	files, err := writeCredentials(context.Background(), nil, req)
	if err != nil {
		t.Fatal(err)
	}

	caData, crt, _ := readKubeconfigCreds(t, files.Kubeconfig)
	if !bytes.Contains(caData, bytes.TrimSpace(f.old.Certs.K8s.Crt)) || !chainsTo(t, crt, f.old.Certs.K8s.Crt) {
		t.Fatal("the kubeconfig made from the encrypted bundle does not match its Kubernetes CA")
	}
}
