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
	"context"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"runtime"
	"testing"
	"time"

	"filippo.io/age"
)

// heapBound is how much the heap may grow above its starting size while a backup is encrypted or an object is
// verified and decrypted. The controller ships with a 512Mi limit; age's default scrypt work factor alone
// costs ~270 MiB per derivation, so this bound fails if the key derivation ever gets expensive again.
const heapBound = 64 << 20

// peakHeapGrowth runs f and returns the highest HeapInuse seen above the value before it started.
func peakHeapGrowth(t *testing.T, f func()) uint64 {
	t.Helper()

	runtime.GC()

	var ms runtime.MemStats

	runtime.ReadMemStats(&ms)

	base, peak := ms.HeapInuse, ms.HeapInuse
	stop, done := make(chan struct{}), make(chan struct{})

	go func() {
		defer close(done)

		var s runtime.MemStats

		for {
			runtime.ReadMemStats(&s)
			peak = max(peak, s.HeapInuse)

			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	f()
	close(stop)
	<-done

	return peak - base
}

func bigSnapshot(t *testing.T, size int) []byte {
	t.Helper()

	db := make([]byte, size-sha256.Size)
	if _, err := rand.Read(db); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(db)

	return append(db, sum[:]...)
}

func TestMemoryBackupAndRestoreOfLargeObject(t *testing.T) {
	const size = 16<<20 + sha256.Size

	snap := bigSnapshot(t, size)
	ctx := context.Background()

	var sealed bytes.Buffer

	grew := peakHeapGrowth(t, func() {
		enc, err := Encrypt(&sealed, "pw")
		if err != nil {
			t.Fatal(err)
		}

		if _, err := io.Copy(enc, bytes.NewReader(snap)); err != nil {
			t.Fatal(err)
		}

		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("encrypt 16 MiB: heap grew by %d MiB (ciphertext buffer included: %d MiB)", grew>>20, sealed.Len()>>20)

	if grew > heapBound+uint64(sealed.Len()) {
		t.Fatalf("encrypting grew the heap by %d MiB, bound %d MiB", grew>>20, heapBound>>20)
	}

	st := memStore{"k": sealed.Bytes()}

	grew = peakHeapGrowth(t, func() {
		// The live restore does exactly this: verify once, then open a reader and stream it to the node.
		if n, err := VerifyObject(ctx, st, "k", "pw"); err != nil || n != size {
			t.Fatalf("verify: %d, %v", n, err)
		}

		rd, err := Reader(ctx, st, "k", "pw")
		if err != nil {
			t.Fatal(err)
		}

		defer rd.Close() //nolint:errcheck

		if _, err := io.Copy(io.Discard, rd); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("verify + read 16 MiB: heap grew by %d MiB", grew>>20)

	if grew > heapBound {
		t.Fatalf("verify+decrypt grew the heap by %d MiB, bound %d MiB", grew>>20, heapBound>>20)
	}
}

// TestBackupsWrittenWithTheDefaultWorkFactorStillDecrypt keeps backups taken before the work factor was
// lowered (they were sealed with age's default, 2^18) restorable.
func TestBackupsWrittenWithTheDefaultWorkFactorStillDecrypt(t *testing.T) {
	r, err := age.NewScryptRecipient("pw")
	if err != nil {
		t.Fatal(err)
	}

	var sealed bytes.Buffer

	w, err := age.Encrypt(&sealed, r)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = w.Write([]byte("old"))
	_ = w.Close()

	rd, err := Decrypt(&sealed, "pw")
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := io.ReadAll(rd); string(got) != "old" {
		t.Fatalf("got %q", got)
	}
}
