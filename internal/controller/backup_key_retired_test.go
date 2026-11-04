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

package controller

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const backupKeyBundle = "backup-key-bundle"

// The backup key of a cluster that rotated its secretbox key must still open backups sealed before.
func TestBackupKeyReadsBackupsSealedBeforeASecretboxRotation(t *testing.T) {
	contract, err := coreconfig.ParseContractFromVersion("v1.14")
	if err != nil {
		t.Fatal(err)
	}

	before, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatal(err)
	}

	oldKey, err := etcdbackup.DeriveKey(before)
	if err != nil {
		t.Fatal(err)
	}

	var sealedOld bytes.Buffer

	w, err := etcdbackup.Encrypt(&sealedOld, oldKey)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write([]byte("etcd snapshot")); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	kr, err := secretsbundle.NewKeyRotation(before, []string{secretsbundle.TargetSecretbox})
	if err != nil {
		t.Fatal(err)
	}

	after := kr.Apply(before)

	var retired secretsbundle.Retired

	retired.Add(secretsbundle.RetiredKey{RetiredAt: time.Now().UTC(), Secretbox: before.Secrets.SecretboxEncryptionSecret})

	rawBundle, err := yaml.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}

	rawRetired, err := retired.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	cluster := &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: "ns",
		Spec: ichorv1alpha1.TalosClusterSpec{SecretsRef: corev1.LocalObjectReference{Name: backupKeyBundle}},
	}

	secret := &corev1.Secret{
		Name: backupKeyBundle, Namespace: trustNamespace,
		Data: map[string][]byte{secretsbundle.SecretKey: rawBundle, secretsbundle.RetiredKeysKey: rawRetired},
	}

	var scrub scrubber

	key, err := backupKey(context.Background(), fake.NewClientBuilder().WithObjects(secret).Build(), trustNamespace, cluster, after, &scrub)
	if err != nil {
		t.Fatal(err)
	}

	r, err := etcdbackup.Decrypt(bytes.NewReader(sealedOld.Bytes()), key)
	if err != nil {
		t.Fatalf("a backup sealed before the rotation must open: %v", err)
	}

	if got, err := io.ReadAll(r); err != nil || string(got) != "etcd snapshot" {
		t.Fatalf("round trip: %q %v", got, err)
	}

	// Every secret the derivation touched is scrubbed from messages, the retired one included.
	msg := scrub.clean(strings.Join([]string{before.Secrets.SecretboxEncryptionSecret, key.Current, key.Retired[0]}, " "))
	if strings.Contains(msg, before.Secrets.SecretboxEncryptionSecret) || strings.Contains(msg, key.Retired[0]) || strings.Contains(msg, key.Current) {
		t.Fatalf("the scrubber left key material in %q", msg)
	}

	// No retired-keys.yaml: the key is what DeriveKey says.
	delete(secret.Data, secretsbundle.RetiredKeysKey)

	plain, err := backupKey(context.Background(), fake.NewClientBuilder().WithObjects(secret).Build(), trustNamespace, cluster, after, &scrubber{})
	if err != nil || len(plain.Retired) != 0 {
		t.Fatalf("a cluster that never rotated has no retired passphrase: %v %v", plain.Retired, err)
	}
}
