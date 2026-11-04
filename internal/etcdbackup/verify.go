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
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
)

// Verifier is an io.Writer that checks an etcd snapshot as it streams past.
// A snapshot from etcd's Maintenance.Snapshot is the bbolt database followed
// by the 32-byte SHA-256 of that database (etcd client/v3/snapshot); Talos
// 1.14.3's own `talosctl etcd snapshot` checks only len%512 == 32, Verifier
// also checks the hash. It does not open the database, so it proves the
// stream was complete and uncorrupted in transit, not that etcd can load it.
type Verifier struct {
	h    hash.Hash
	tail []byte // the last up-to-32 bytes seen, held back from the hash
	n    int64
}

// NewVerifier returns an empty Verifier.
func NewVerifier() *Verifier { return &Verifier{h: sha256.New()} }

// Write implements io.Writer. It never fails.
func (v *Verifier) Write(p []byte) (int, error) {
	v.n += int64(len(p))
	v.tail = append(v.tail, p...)

	if extra := len(v.tail) - sha256.Size; extra > 0 {
		v.h.Write(v.tail[:extra])
		v.tail = append(v.tail[:0], v.tail[extra:]...)
	}

	return len(p), nil
}

// Size is the number of bytes seen.
func (v *Verifier) Size() int64 { return v.n }

// Verify reports whether the bytes written so far form a complete snapshot.
func (v *Verifier) Verify() error {
	if v.n%512 != sha256.Size {
		return fmt.Errorf("snapshot is %d bytes, not a bbolt file plus a sha256 trailer", v.n)
	}

	if !bytes.Equal(v.h.Sum(nil), v.tail) {
		return errors.New("snapshot checksum mismatch")
	}

	return nil
}
