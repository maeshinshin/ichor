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
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// rotatedKeys returns the bundle after a secretbox rotation and the retired list that rotation leaves.
func rotatedKeys(t *testing.T, old *secrets.Bundle) (rotated *secrets.Bundle, retired *secretsbundle.Retired) {
	t.Helper()

	kr, err := secretsbundle.NewKeyRotation(old, []string{secretsbundle.TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	retired = &secretsbundle.Retired{}
	retired.Add(secretsbundle.RetiredKey{RetiredAt: time.Unix(1000, 0).UTC(), Secretbox: old.Secrets.SecretboxEncryptionSecret})

	return kr.Apply(old), retired
}

// exportDeps are bundle-export's real savers over ~/.ichor of a temporary HOME: sealed as the command seals
// (plaintext when the test says so).
func exportDeps(k8s client.Client, out, errw *bytes.Buffer, plaintext bool) bundleExportDeps {
	return bundleExportDeps{
		K8s: k8s,
		Save: func(cluster string, bundle *secrets.Bundle) (string, error) {
			return saveSealedBundle(cluster, bundle, plaintext, true, errw)
		},
		Load: loadBundleFile,
		SaveRetired: func(cluster string, retired *secretsbundle.Retired, bundlePath string) (string, error) {
			return saveRetiredFile(cluster, retired, bundlePath, errw)
		},
		LoadRetired: loadRetiredFile, Stdout: out,
	}
}

func exportRotated(t *testing.T, deps func(client.Client) bundleExportDeps) (name string, rotated *secrets.Bundle, retired *secretsbundle.Retired, k8s client.Client) {
	t.Helper()

	rotated, retired = rotatedKeys(t, newTestBundle(t))

	rawRetired, err := retired.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	k8s, name = bundleExportFixture(t, rotated, map[string][]byte{secretsbundle.RetiredKeysKey: rawRetired})

	if err := runBundleExport(context.Background(), deps(k8s), testNamespace, testNamespace, name); err != nil {
		t.Fatal(err)
	}

	return name, rotated, retired, k8s
}

func TestBundleExportSavesThePlaintextRetiredKeysOfAPlaintextBundleWithTheSameWarning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useBundleCrypto(t)

	var out, errw bytes.Buffer

	name, rotated, retired, k8s := exportRotated(t, func(c client.Client) bundleExportDeps { return exportDeps(c, &out, &errw, true) })

	dir, err := bundleDir(name)
	if err != nil {
		t.Fatal(err)
	}

	bundlePath := dir + "/" + secretsbundle.SecretKey

	saved, err := loadRetiredFile(bundlePath)
	if err != nil || len(saved.Keys) != 1 || saved.Keys[0].Secretbox != retired.Keys[0].Secretbox {
		t.Fatalf("the retired keys were not saved next to the bundle: %+v %v", saved, err)
	}

	raw, err := os.ReadFile(retiredFilePath(bundlePath))
	if err != nil || isEncryptedBundle(raw) {
		t.Fatalf("a plaintext bundle's retired keys are plaintext: %v", err)
	}

	if st, err := os.Stat(retiredFilePath(bundlePath)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the retired keys file must be 0600: %v %v", st, err)
	}

	if !strings.Contains(errw.String(), secretsbundle.RetiredKeysKey) || !strings.Contains(errw.String(), "unencrypted") {
		t.Fatalf("storing the retired keys in plaintext must warn like the bundle does: %s", errw.String())
	}

	assertAcknowledged(t, k8s, name, rotated, retired)

	if !strings.Contains(out.String(), secretsbundle.RetiredKeysKey) {
		t.Fatalf("the command must say the retired keys were saved: %s", out.String())
	}

	for _, s := range append(bundleSecretMaterial(rotated), retired.Keys[0].Secretbox) {
		if strings.Contains(out.String(), s) || strings.Contains(errw.String(), s) {
			t.Fatal("the command printed key material")
		}
	}
}

func assertAcknowledged(t *testing.T, k8s client.Client, name string, rotated *secrets.Bundle, retired *secretsbundle.Retired) {
	t.Helper()

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &cluster); err != nil {
		t.Fatal(err)
	}

	want, err := secretsbundle.ExportFingerprint(rotated, retired)
	if err != nil {
		t.Fatal(err)
	}

	plain, _ := secretsbundle.BundleFingerprint(rotated)
	if want == plain || cluster.Annotations[ichorv1alpha1.BundleExportedAnnotation] != want {
		t.Fatalf("the exported fingerprint must cover the retired keys: %v", cluster.Annotations)
	}
}

// ADR 0127: the retired keys are key material and are sealed under the bundle's passphrase, from the same sources.
func TestBundleExportSealsTheRetiredKeysUnderTheBundlesPassphrase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useBundleCrypto(t)
	t.Setenv(bundlePassphraseEnv, testBundlePass)

	var out, errw bytes.Buffer

	name, rotated, retired, k8s := exportRotated(t, func(c client.Client) bundleExportDeps { return exportDeps(c, &out, &errw, false) })

	dir, err := bundleDir(name)
	if err != nil {
		t.Fatal(err)
	}

	bundlePath := dir + "/" + secretsbundle.SecretKey

	raw, err := os.ReadFile(retiredFilePath(bundlePath))
	if err != nil {
		t.Fatal(err)
	}

	if !isEncryptedBundle(raw) || bytes.Contains(raw, []byte(retired.Keys[0].Secretbox)) || bytes.Contains(raw, []byte("retired")) {
		t.Fatalf("the retired keys must be an age file with no readable content: %.60q", raw)
	}

	if st, err := os.Stat(retiredFilePath(bundlePath)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the retired keys file must be 0600: %v %v", st, err)
	}

	// It opens with the passphrase of the bundle, as `age -d` would, and not with another.
	plain, err := openBundleBytes(raw, testBundlePass)
	if err != nil {
		t.Fatalf("the retired keys must open under the bundle's passphrase: %v", err)
	}

	if _, err := secretsbundle.ParseRetired(plain); err != nil {
		t.Fatal(err)
	}

	if _, err := openBundleBytes(raw, testBundleWrongPass); err == nil {
		t.Fatal("the retired keys opened under another passphrase")
	}

	if strings.Contains(errw.String(), "unencrypted") {
		t.Fatalf("an encrypted export must not warn about plaintext: %s", errw.String())
	}

	assertAcknowledged(t, k8s, name, rotated, retired)

	// A fresh process opens both with the passphrase from the sources and reads the list back.
	clear(sealedThisRun)
	clear(openedThisRun)

	saved, err := loadRetiredFile(bundlePath)
	if err != nil || len(saved.Keys) != 1 || saved.Keys[0].Secretbox != retired.Keys[0].Secretbox {
		t.Fatalf("encrypted retired keys were not read back: %+v %v", saved, err)
	}

	// Another passphrase is an error that names neither of them.
	t.Setenv(bundlePassphraseEnv, testBundleWrongPass)

	if _, err := loadRetiredFile(bundlePath); err == nil || strings.Contains(err.Error(), testBundleWrongPass) || strings.Contains(err.Error(), testBundlePass) {
		t.Fatalf("a wrong passphrase must be refused without naming it: %v", err)
	}

	for _, s := range []string{testBundlePass, retired.Keys[0].Secretbox} {
		if strings.Contains(out.String(), s) || strings.Contains(errw.String(), s) {
			t.Fatal("the command printed a passphrase or key material")
		}
	}
}

func TestBundleExportRefusesToAcknowledgeWithoutBeingAbleToSaveRetiredKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useBundleCrypto(t)

	rotated, retired := rotatedKeys(t, newTestBundle(t))
	rawRetired, _ := retired.Marshal()
	k8s, name := bundleExportFixture(t, rotated, map[string][]byte{secretsbundle.RetiredKeysKey: rawRetired})

	deps := bundleExportDeps{K8s: k8s, Save: savePlainBundle, Load: loadBundleFile, Stdout: &bytes.Buffer{}}
	if err := runBundleExport(context.Background(), deps, testNamespace, testNamespace, name); err == nil {
		t.Fatal("an export that cannot keep the retired keys was acknowledged")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &cluster); err != nil {
		t.Fatal(err)
	}

	if _, marked := cluster.Annotations[ichorv1alpha1.BundleExportedAnnotation]; marked {
		t.Fatal("the cluster was marked as exported without its retired keys")
	}
}

// The retired keys cannot be sealed under a passphrase nobody knows: an encrypted bundle whose passphrase this
// process does not hold is refused rather than followed by a plaintext file.
func TestSavingRetiredKeysNextToAnEncryptedBundleWithoutItsPassphraseIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useBundleCrypto(t)

	path, err := saveBundleFile("enc", newTestBundle(t), testBundlePass)
	if err != nil {
		t.Fatal(err)
	}

	var errw bytes.Buffer

	if _, err := saveRetiredFile("enc", &secretsbundle.Retired{}, path, &errw); err == nil {
		t.Fatal("plaintext retired keys were written next to an encrypted bundle")
	}
}

// A total-loss restore with the bundle exported after a key rotation opens a snapshot sealed before it (ADR 0121),
// whether the bundle and the retired keys are encrypted (ADR 0127) or not.
func TestRestoreOpensASnapshotSealedBeforeAKeyRotation(t *testing.T) {
	for name, pass := range map[string]string{"plaintext": "", "encrypted": testBundlePass} {
		t.Run(name, func(t *testing.T) {
			h := newRestoreHarness(t)
			useBundleCrypto(t)

			rotated, retired := rotatedKeys(t, h.bundle)

			snapshot := goodSnapshot(t)
			putSnapshot(t, h.store, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), snapshot, h.bundle)

			// What `ichor cluster bundle-export` leaves after the rotation.
			path, err := saveBundleFile("lost", rotated, pass)
			if err != nil {
				t.Fatal(err)
			}

			if pass != "" {
				t.Setenv(bundlePassphraseEnv, pass)
				sealedThisRun[path] = pass
			}

			if err := h.run(); err == nil {
				t.Fatal("the snapshot opened without the retired keys")
			}

			if _, err := saveRetiredFile("lost", retired, path, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}

			// A new process: nothing remembered, the passphrase comes from the sources.
			clear(sealedThisRun)
			clear(openedThisRun)

			h.talos = &fakeRecover{}
			h.deps.Dial = func(context.Context, string, string, *secrets.Bundle) (recoverClient, error) { return h.talos, nil }

			if err := h.run(); err != nil {
				t.Fatalf("with the retired keys next to the bundle the snapshot must open: %v", err)
			}

			if !bytes.Equal(h.talos.uploaded, snapshot) {
				t.Fatal("Talos did not receive the snapshot")
			}

			for _, s := range []string{retired.Keys[0].Secretbox, pass} {
				if s != "" && strings.Contains(h.stdout.String()+h.stderr.String(), s) {
					t.Fatal("output contains a retired secret or the passphrase")
				}
			}
		})
	}
}

// `ichor secrets encrypt` seals the retired keys next to a plaintext bundle under the same passphrase.
func TestSecretsEncryptSealsTheRetiredKeysNextToTheBundle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	useBundleCrypto(t)
	t.Setenv(bundlePassphraseEnv, testBundlePass)

	rotated, retired := rotatedKeys(t, newTestBundle(t))

	path, err := saveBundleFile("old", rotated, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := saveRetiredFile("old", retired, path, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	if err := runSecretsEncrypt(path, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(retiredFilePath(path))
	if err != nil || !isEncryptedBundle(raw) {
		t.Fatalf("the retired keys were left in plaintext: %v", err)
	}

	clear(sealedThisRun)
	clear(openedThisRun)

	saved, err := loadRetiredFile(path)
	if err != nil || len(saved.Keys) != 1 || saved.Keys[0].Secretbox != retired.Keys[0].Secretbox {
		t.Fatalf("sealed retired keys did not read back: %+v %v", saved, err)
	}
}
