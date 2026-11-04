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
	"maps"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const exportBundleSecret = "export-bundle"

func bundleExportFixture(t *testing.T, bundle *secrets.Bundle, extra map[string][]byte) (client.Client, string) {
	t.Helper()

	raw, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}

	data := map[string][]byte{secretsbundle.SecretKey: raw}
	maps.Copy(data, extra)

	cluster := newTestCluster()
	cluster.Spec.SecretsRef.Name = exportBundleSecret

	secret := &corev1.Secret{Namespace: testNamespace, Name: exportBundleSecret, Data: data}

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, secret).Build(), cluster.Name
}

func TestBundleExportSavesReadsBackAndAcknowledges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	bundle := newTestBundle(t)
	k8s, name := bundleExportFixture(t, bundle, nil)

	var out bytes.Buffer

	err := runBundleExport(context.Background(),
		bundleExportDeps{K8s: k8s, Save: savePlainBundle, Load: loadBundleFile, Stdout: &out}, testNamespace, testNamespace, name)
	if err != nil {
		t.Fatal(err)
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &cluster); err != nil {
		t.Fatal(err)
	}

	want, err := secretsbundle.ExportFingerprint(bundle, nil)
	if err != nil {
		t.Fatal(err)
	}

	if cluster.Annotations[ichorv1alpha1.BundleExportedAnnotation] != want {
		t.Fatalf("the cluster was not marked with the exported bundle's fingerprint: %v", cluster.Annotations)
	}

	for _, s := range bundleSecretMaterial(bundle) {
		if strings.Contains(out.String(), s) {
			t.Fatal("the command printed bundle material")
		}
	}
}

func TestBundleExportDoesNotAcknowledgeWhatItCouldNotSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	k8s, name := bundleExportFixture(t, newTestBundle(t), nil)

	// A file that does not hold the live bundle (here: some other bundle) must not be acknowledged.
	other := newFreshTestBundle(t)
	deps := bundleExportDeps{
		K8s: k8s, Stdout: &bytes.Buffer{},
		Save: func(cluster string, _ *secrets.Bundle) (string, error) { return saveBundleFile(cluster, other, "") },
		Load: loadBundleFile,
	}

	if err := runBundleExport(context.Background(), deps, testNamespace, testNamespace, name); err == nil {
		t.Fatal("a mismatching file was acknowledged")
	}

	deps.Save = func(string, *secrets.Bundle) (string, error) { return "", errors.New("disk full") }
	if err := runBundleExport(context.Background(), deps, testNamespace, testNamespace, name); err == nil {
		t.Fatal("a failed save was acknowledged")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &cluster); err != nil {
		t.Fatal(err)
	}

	if _, marked := cluster.Annotations[ichorv1alpha1.BundleExportedAnnotation]; marked {
		t.Fatal("the cluster was marked as exported although nothing was saved")
	}
}

func TestBundleExportRefusesWhileARotationIsUnderWay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	k8s, name := bundleExportFixture(t, newTestBundle(t), map[string][]byte{secretsbundle.RotationKey: []byte("x")})

	err := runBundleExport(context.Background(),
		bundleExportDeps{K8s: k8s, Save: savePlainBundle, Load: loadBundleFile, Stdout: &bytes.Buffer{}}, testNamespace, testNamespace, name)
	if err == nil || !strings.Contains(err.Error(), "rotation") {
		t.Fatalf("export during a rotation must be refused, got %v", err)
	}
}
