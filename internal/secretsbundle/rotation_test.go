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

package secretsbundle

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	corev1 "k8s.io/api/core/v1"
)

func TestRotationRoundTripAndApply(t *testing.T) {
	bundle := newTestBundle(t)

	contract, err := coreconfig.ParseContractFromVersion("v1.14")
	if err != nil {
		t.Fatal(err)
	}

	rot, err := NewRotation(time.Now(), contract)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := rot.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	back, err := ParseRotation(raw)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(back.Talos.Key, rot.Talos.Key) || !bytes.Equal(back.Kubernetes.Key, rot.Kubernetes.Key) {
		t.Fatal("the new CA keys did not survive the round trip")
	}

	applied := rot.Apply(bundle)
	if !bytes.Equal(applied.Certs.OS.Crt, rot.Talos.Crt) || !bytes.Equal(applied.Certs.K8s.Crt, rot.Kubernetes.Crt) {
		t.Fatal("Apply did not replace the CAs")
	}

	if applied.Cluster != bundle.Cluster || applied.Certs.Etcd != bundle.Certs.Etcd {
		t.Fatal("Apply changed something other than the Talos and Kubernetes CAs")
	}

	if bytes.Equal(bundle.Certs.OS.Crt, rot.Talos.Crt) {
		t.Fatal("Apply modified its input")
	}

	oldFP, _ := BundleFingerprint(bundle)
	newFP, _ := BundleFingerprint(applied)

	if oldFP == "" || oldFP == newFP {
		t.Fatalf("fingerprints must differ across a rotation: %q %q", oldFP, newFP)
	}
}

func TestParseRotationErrorsCarryNoContent(t *testing.T) {
	_, err := ParseRotation([]byte("talos:\n  crt: c2VjcmV0LWNydA==\n  key: c2VjcmV0LWtleQ==\n"))
	if err == nil {
		t.Fatal("a rotation without a valid certificate must be refused")
	}

	if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "c2VjcmV0") {
		t.Fatalf("error carries key material: %v", err)
	}
}

func TestLoadRotationAbsentAndPresent(t *testing.T) {
	secret := &corev1.Secret{Namespace: testNamespace, Name: testName, Data: map[string][]byte{}}
	c := newFakeClient(secret)

	got, err := LoadRotation(context.Background(), c, testNamespace, testName)
	if err != nil || got != nil {
		t.Fatalf("no rotation.yaml means no rotation, got %v %v", got, err)
	}

	contract, _ := coreconfig.ParseContractFromVersion("v1.14")
	rot, _ := NewRotation(time.Now(), contract)
	raw, _ := rot.Marshal()
	secret.Data[RotationKey] = raw

	c = newFakeClient(secret)

	got, err = LoadRotation(context.Background(), c, testNamespace, testName)
	if err != nil || got == nil || !bytes.Equal(got.Talos.Crt, rot.Talos.Crt) {
		t.Fatalf("rotation.yaml was not read back: %v %v", got, err)
	}
}
