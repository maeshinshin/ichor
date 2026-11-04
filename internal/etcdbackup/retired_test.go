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

package etcdbackup

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// A rotation of the secretbox key changes the key new backups are sealed with. The secret it retired is
// kept in the bundle, and a backup sealed with the key derived from it must stay readable.
func TestRetiredSecretboxKeysKeepOlderBackupsReadable(t *testing.T) {
	old := testBundle(t, "cluster-secret")
	oldSecret := old.Secrets.SecretboxEncryptionSecret

	oldKey, err := DeriveKey(old)
	if err != nil {
		t.Fatal(err)
	}

	// The bundle after the rotation: same cluster, a new secretbox secret.
	rotated := testBundle(t, "cluster-secret")
	rotated.Secrets = &secrets.Secrets{SecretboxEncryptionSecret: "box-rotated"}

	plain := makeSnapshot("etcd data")
	before := sealed(t, plain, oldKey)

	open := func(enc []byte, k Key) error {
		r, err := Decrypt(bytes.NewReader(enc), k)
		if err != nil {
			return err
		}

		got, err := io.ReadAll(r)
		if err == nil && !bytes.Equal(got, plain) {
			err = errors.New("plaintext differs")
		}

		return err
	}

	withoutRetired, err := DeriveKey(rotated)
	if err != nil {
		t.Fatal(err)
	}

	if withoutRetired.Current == oldKey.Current {
		t.Fatal("a new secretbox secret must derive a new backup key")
	}

	if err := open(before, withoutRetired); err == nil {
		t.Fatal("a backup sealed before the rotation opened without the retired secret")
	}

	key, err := DeriveKey(rotated, "box-older", oldSecret, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(key.Retired) != 2 || key.Retired[1] != oldKey.Current {
		t.Fatalf("an empty retired secret must be skipped and the rest derived like the current key: %v", key.Retired)
	}

	if err := open(before, key); err != nil {
		t.Fatalf("a backup sealed before the rotation must stay readable with the retired secret: %v", err)
	}

	// New backups are sealed with the current key alone, and the retired ones are only tried when reading.
	if key.Current != withoutRetired.Current || key.Legacy != withoutRetired.Legacy {
		t.Fatal("retired secrets changed the current or the legacy key")
	}

	if err := open(sealed(t, plain, key), Key{Current: key.Current}); err != nil {
		t.Fatalf("a new backup must open with the current key alone: %v", err)
	}

	if got := key.Secrets(); len(got) != 4 {
		t.Fatalf("Secrets must list the current, the legacy and every retired passphrase, got %d", len(got))
	}
}

// The retired list is part of what a scrubber must blank, but a Key without any must not list empty strings.
func TestKeySecretsSkipsNothingElseAndNoRetired(t *testing.T) {
	key, err := DeriveKey(testBundle(t, "cluster-secret"))
	if err != nil {
		t.Fatal(err)
	}

	if len(key.Retired) != 0 || len(key.Secrets()) != 2 {
		t.Fatalf("a bundle that never rotated has no retired passphrase: %v", key.Retired)
	}
}
