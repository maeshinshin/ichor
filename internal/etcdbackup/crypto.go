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
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// hkdfInfo domain-separates the backup key from any other use of the bundle.
// Changing it makes every existing backup undecryptable: it is part of the
// format, bump the "v1" only together with a new key-derivation scheme.
const hkdfInfo = "ichor.maesh.dev/etcd-backup/v1"

// Passphrase derives the age passphrase snapshots are encrypted with from the
// cluster's secrets bundle. Nothing about it is stored anywhere: whoever holds
// the bundle can derive it again, which is what lets "bundle + snapshot" be
// enough to recover (design, "Backup, recovery, certificates"), and whoever
// has only the bucket cannot.
//
// The result is secret material: never log it or put it in an error.
func Passphrase(bundle *secrets.Bundle) (string, error) {
	if bundle == nil || bundle.Cluster == nil || bundle.Cluster.Secret == "" {
		return "", errors.New("secrets bundle carries no cluster secret to derive the backup key from")
	}

	key, err := hkdf.Key(sha256.New, []byte(bundle.Cluster.Secret), []byte(bundle.Cluster.ID), hkdfInfo, 32)
	if err != nil {
		return "", fmt.Errorf("deriving backup key: %w", err)
	}

	return hex.EncodeToString(key), nil
}

// encryptWorkFactor is log2 of scrypt's N, r=8 (1 MiB). The passphrase is a 256-bit HKDF output, not something
// a person chose, so scrypt's job of slowing down guessing is moot; age's default (18, ~270 MiB of heap per
// derivation) would OOM the controller. No backup was ever sealed with another factor (pre-release), so
// Decrypt caps at the same value.
const encryptWorkFactor = 10

// Encrypt returns a writer that age-encrypts (scrypt recipient) into dst. The
// caller must Close it to flush the final chunk.
func Encrypt(dst io.Writer, passphrase string) (io.WriteCloser, error) {
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}

	r.SetWorkFactor(encryptWorkFactor)

	return age.Encrypt(dst, r)
}

// Decrypt is Encrypt's inverse. A wrong key and a corrupted file both fail
// with age's own error, which names neither the passphrase nor any content.
func Decrypt(src io.Reader, passphrase string) (io.Reader, error) {
	i, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}

	i.SetMaxWorkFactor(encryptWorkFactor)

	return age.Decrypt(src, i)
}
