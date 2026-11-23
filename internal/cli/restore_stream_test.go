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
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

const streamMiB = 1 << 20

// genStore serves one encrypted snapshot of mib MiB that is generated while it is read, so no copy of it
// exists in the test either. flip damages one body byte to break the checksum.
type genStore struct {
	memStore
	pass etcdbackup.Key
	mib  int
	flip bool
}

func (g *genStore) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()

	go func() {
		w, err := etcdbackup.Encrypt(pw, g.pass)
		if err != nil {
			pw.CloseWithError(err)

			return
		}

		h := sha256.New()
		block := make([]byte, streamMiB)

		for i := range g.mib {
			block[0] = byte(i)
			h.Write(block)

			if g.flip && i == 0 {
				block[1] ^= 0xff
			}

			if _, err := w.Write(block); err != nil {
				pw.CloseWithError(err)

				return
			}

			block[1] = 0
		}

		_, _ = w.Write(h.Sum(nil))
		pw.CloseWithError(w.Close())
	}()

	return pr, nil
}

func (g *genStore) List(context.Context, string) ([]etcdbackup.Object, error) {
	return []etcdbackup.Object{{
		Key: "k", Name: etcdbackup.ObjectName(time.Unix(1e9, 0)), Time: time.Unix(1e9, 0),
	}}, nil
}

// peakRecover drains the upload in small reads and records the largest live heap it saw, after a GC.
type peakRecover struct {
	fakeRecover
	got, peak uint64
	base      uint64 // live heap before the restore started
}

func (p *peakRecover) EtcdRecover(_ context.Context, r io.Reader) error {
	p.order = append(p.order, "recover")

	buf := make([]byte, 32<<10)

	for i := 0; ; i++ {
		n, err := r.Read(buf)
		p.got += uint64(n)

		if i%128 == 0 {
			var m runtime.MemStats

			runtime.GC()
			runtime.ReadMemStats(&m)
			p.peak = max(p.peak, m.HeapAlloc-min(m.HeapAlloc, p.base))
		}

		if err == io.EOF {
			return nil
		}

		if err != nil {
			return err
		}
	}
}

func TestRestoreLargeSnapshotIsNeverHeldInMemory(t *testing.T) {
	const mib = 96

	h := newRestoreHarness(t)
	pass, _ := etcdbackup.DeriveKey(h.bundle)
	gs := &genStore{pass: pass, mib: mib}
	h.deps.NewStore = func(etcdbackup.S3Config) (etcdbackup.Store, error) { return gs, nil }
	pr := &peakRecover{}

	var m runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&m)
	pr.base = m.HeapAlloc
	h.deps.Dial = func(context.Context, string, string, *secrets.Bundle) (recoverClient, error) { return pr, nil }

	if err := h.run(); err != nil {
		t.Fatal(err)
	}

	if want := uint64(mib*streamMiB + sha256.Size); pr.got != want {
		t.Fatalf("uploaded %d bytes, want %d", pr.got, want)
	}

	if limit := uint64(16 * streamMiB); pr.peak > limit {
		t.Fatalf("peak live heap during the upload was %d MiB of a %d MiB snapshot, limit %d MiB",
			pr.peak/streamMiB, mib, limit/streamMiB)
	}
}

func TestRestoreStreamedChecksumMismatchSendsNothingAndLeavesNoFile(t *testing.T) {
	h := newRestoreHarness(t)
	tmp := scratchDir(t)
	pass, _ := etcdbackup.DeriveKey(h.bundle)
	gs := &genStore{pass: pass, mib: 8, flip: true}
	h.deps.NewStore = func(etcdbackup.S3Config) (etcdbackup.Store, error) { return gs, nil }

	if err := h.run(); err == nil {
		t.Fatal("a snapshot with a bad checksum was restored")
	}

	if len(h.talos.order) != 0 {
		t.Fatalf("the node was contacted: %v", h.talos.order)
	}

	assertEmptyDir(t, tmp)
}

func TestRestoreLeavesNoFileOnSuccess(t *testing.T) {
	h := newRestoreHarness(t)
	tmp := scratchDir(t)
	putSnapshot(t, h.store, time.Unix(1e9, 0), goodSnapshot(t), h.bundle)

	if err := h.run(); err != nil {
		t.Fatal(err)
	}

	assertEmptyDir(t, tmp)
}

// scratchDir is an empty TMPDIR under the test's HOME, which the harness keeps inside the test's own tree.
func scratchDir(t *testing.T) string {
	t.Helper()

	d := filepath.Join(os.Getenv("HOME"), "tmp")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TMPDIR", d)

	return d
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("%s holds %d leftover entries, first %q", dir, len(es), es[0].Name())
	}
}
