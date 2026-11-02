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

// Package etcdbackup holds the storage format of an etcd snapshot backup
// (ADR 0047): how a snapshot is verified, encrypted, named and stored. It is
// shared by the TalosEtcdBackup controller (writer) and the restore path
// (reader), so the two cannot drift apart.
package etcdbackup

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// The HKDF info strings domain-separate the backup key from any other use of the bundle. They are part of
// the format: changing one makes every backup sealed with it undecryptable.
const (
	// hkdfInfoV1 derived the key from Cluster.Secret, which Talos writes into every worker machine
	// config, so a worker node plus the bucket was enough to decrypt a backup. It is kept only to read
	// backups sealed before ADR 0108.
	hkdfInfoV1 = "ichor.maesh.dev/etcd-backup/v1"

	// hkdfInfoV2 derives the key from a secret only control planes hold (ADR 0108).
	hkdfInfoV2 = "ichor.maesh.dev/etcd-backup/v2"
)

// Key is the pair of passphrases of one cluster: Current seals new backups, Legacy (v1) is only tried when
// reading. Nothing about either is stored anywhere: whoever holds the bundle can derive them again, which is
// what lets "bundle + snapshot" be enough to recover (design, "Backup, recovery, certificates"), and
// whoever has only the bucket cannot.
//
// The values are secret material: never log them or put them in an error. Secrets lists both for a scrubber.
type Key struct{ Current, Legacy string }

// Secrets is every passphrase of k, for a scrubber.
func (k Key) Secrets() []string { return []string{k.Current, k.Legacy} }

// DeriveKey derives the backup passphrases from the cluster's secrets bundle. The v2 input is the
// secretbox key (then the legacy AES-CBC key) of the Kubernetes API server: control planes alone hold it,
// and Ichor never rotates it, so it keeps older backups readable across a CA rotation (ADR 0063). The v1
// input, Cluster.Secret, is in every worker's machine config.
func DeriveKey(bundle *secrets.Bundle) (Key, error) {
	if bundle == nil || bundle.Cluster == nil || bundle.Cluster.Secret == "" {
		return Key{}, errors.New("secrets bundle carries no cluster secret to derive the backup key from")
	}

	var ikm string

	if bundle.Secrets != nil {
		ikm = bundle.Secrets.SecretboxEncryptionSecret
		if ikm == "" {
			ikm = bundle.Secrets.AESCBCEncryptionSecret
		}
	}

	if ikm == "" {
		return Key{}, errors.New("secrets bundle carries no control-plane-only secret (secretbox key) to derive the backup key from")
	}

	v1, err := hkdf.Key(sha256.New, []byte(bundle.Cluster.Secret), []byte(bundle.Cluster.ID), hkdfInfoV1, 32)
	if err != nil {
		return Key{}, fmt.Errorf("deriving backup key: %w", err)
	}

	v2, err := hkdf.Key(sha256.New, []byte(ikm), []byte(bundle.Cluster.ID), hkdfInfoV2, 32)
	if err != nil {
		return Key{}, fmt.Errorf("deriving backup key: %w", err)
	}

	return Key{Current: hex.EncodeToString(v2), Legacy: hex.EncodeToString(v1)}, nil
}

// encryptWorkFactor is log2 of scrypt's N, r=8 (1 MiB). The passphrase is a 256-bit HKDF output, not something
// a person chose, so scrypt's job of slowing down guessing is moot; age's default (18, ~270 MiB of heap per
// derivation) would OOM the controller. No backup was ever sealed with another factor (pre-release), so
// Decrypt caps at the same value.
const encryptWorkFactor = 10

// headerProbeBytes is how much of a backup Decrypt buffers so a second passphrase can be tried: an age
// header with one scrypt recipient is a few hundred bytes, and a wrong passphrase fails inside it.
const headerProbeBytes = 4096

// Encrypt returns a writer that age-encrypts (scrypt recipient) into dst. The
// caller must Close it to flush the final chunk.
func Encrypt(dst io.Writer, key Key) (io.WriteCloser, error) {
	r, err := age.NewScryptRecipient(key.Current)
	if err != nil {
		return nil, err
	}

	r.SetWorkFactor(encryptWorkFactor)

	return age.Encrypt(dst, r)
}

// Decrypt is Encrypt's inverse; it opens a backup sealed with either passphrase of key, current first. A
// wrong key and a corrupted file both fail with age's own error, which names neither the passphrase nor any
// content.
func Decrypt(src io.Reader, key Key) (io.Reader, error) {
	// age reads the header before returning, so src must be replayable for the second try.
	buf, err := io.ReadAll(io.LimitReader(src, headerProbeBytes))
	if err != nil {
		return nil, err
	}

	var first error

	for _, pass := range []string{key.Current, key.Legacy} {
		if pass == "" {
			continue
		}

		i, err := age.NewScryptIdentity(pass)
		if err != nil {
			return nil, err
		}

		i.SetMaxWorkFactor(encryptWorkFactor)

		r, err := age.Decrypt(io.MultiReader(bytes.NewReader(buf), src), i)
		if err == nil {
			return r, nil
		}

		if first == nil {
			first = err
		}
	}

	if first == nil {
		first = errors.New("no backup passphrase")
	}

	return nil, first
}
