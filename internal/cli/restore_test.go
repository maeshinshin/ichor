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
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

const (
	testS3Prefix    = "clusters/lost"
	testS3AccessKey = "AKIA-test-access-key"
	testS3SecretKey = "test-s3-secret-key-value"
)

// memStore is an in-memory etcdbackup.Store: the restore logic only sees the
// interface, and etcdbackup's own tests drive the real S3 client against a
// fake S3.
type memStore struct {
	objs    map[string][]byte
	getErr  error
	listErr error
}

func (m *memStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	b, _ := io.ReadAll(r)
	m.objs[key] = b

	return nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]etcdbackup.Object, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}

	var out []etcdbackup.Object

	for k, v := range m.objs {
		name := strings.TrimPrefix(k, prefix+"/")
		if t, ok := etcdbackup.ParseObjectName(name); ok && strings.HasPrefix(k, prefix+"/") {
			out = append(out, etcdbackup.Object{Key: k, Name: name, Time: t, Size: int64(len(v))})
		}
	}

	slices.SortFunc(out, func(a, b etcdbackup.Object) int { return b.Time.Compare(a.Time) })

	return out, nil
}

func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}

	return io.NopCloser(bytes.NewReader(m.objs[key])), nil
}

func (m *memStore) Delete(context.Context, string) error { return nil }

type fakeRecover struct {
	uploaded []byte
	order    []string
	recErr   error
}

func (f *fakeRecover) EtcdRecover(_ context.Context, r io.Reader) error {
	f.order = append(f.order, "recover")
	f.uploaded, _ = io.ReadAll(r)

	return f.recErr
}

func (f *fakeRecover) BootstrapRecover(context.Context) error {
	f.order = append(f.order, "bootstrap")

	return nil
}

// goodSnapshot is what EtcdSnapshot streams: a "bbolt" file (a multiple of
// 512 bytes) plus its sha256.
func goodSnapshot(t *testing.T) []byte {
	t.Helper()

	body := make([]byte, 2048, 2048+sha256.Size)
	if _, err := rand.Read(body); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(body)

	return append(body, sum[:]...)
}

func putSnapshot(t *testing.T, s *memStore, at time.Time, plain []byte, bundle *secrets.Bundle) {
	t.Helper()

	pass, err := etcdbackup.DeriveKey(bundle)
	if err != nil {
		t.Fatal(err)
	}

	var enc bytes.Buffer

	w, err := etcdbackup.Encrypt(&enc, pass)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	key := testS3Prefix + "/" + etcdbackup.ObjectName(at)
	if err := s.Put(context.Background(), key, &enc, int64(enc.Len())); err != nil {
		t.Fatal(err)
	}

}

type restoreHarness struct {
	opts   restoreOptions
	deps   restoreDeps
	store  *memStore
	talos  *fakeRecover
	stdout bytes.Buffer
	stderr bytes.Buffer
	bundle *secrets.Bundle
}

func newRestoreHarness(t *testing.T) *restoreHarness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	h := &restoreHarness{
		store: &memStore{objs: map[string][]byte{}}, talos: &fakeRecover{}, bundle: newTestBundle(t),
	}

	if _, err := saveBundleFile("lost", h.bundle, ""); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"ICHOR_S3_ACCESS_KEY_ID": testS3AccessKey, "ICHOR_S3_SECRET_ACCESS_KEY": testS3SecretKey}
	h.opts = restoreOptions{
		RecoverCluster: true, Cluster: "lost", NodeEndpoint: "10.0.0.9:50000", S3Prefix: testS3Prefix,
		S3: etcdbackup.S3Config{Endpoint: "https://s3.example", Bucket: "b"},
	}
	h.deps = restoreDeps{
		NewStore: func(etcdbackup.S3Config) (etcdbackup.Store, error) { return h.store, nil },
		Dial: func(context.Context, string, string, *secrets.Bundle) (recoverClient, error) {
			return h.talos, nil
		},
		Getenv:   func(k string) string { return env[k] },
		ReadFile: func(string) ([]byte, error) { return nil, errors.New("unused") },
		Stdout:   &h.stdout, Stderr: &h.stderr, Stdin: strings.NewReader(""), AssumeYes: true,
	}

	return h
}

func (h *restoreHarness) run() error {
	return runRecoverCluster(context.Background(), h.opts, h.deps)
}

func (h *restoreHarness) assertNoLeak(t *testing.T, err error) {
	t.Helper()

	pass, _ := etcdbackup.DeriveKey(h.bundle)
	all := append(append(bundleSecretMaterial(h.bundle), pass.Secrets()...), testS3AccessKey, testS3SecretKey)

	hay := h.stdout.String() + h.stderr.String()
	if err != nil {
		hay += err.Error()
	}

	for _, s := range all {
		if s != "" && strings.Contains(hay, s) {
			t.Fatalf("output contains secret material:\n%s", hay)
		}
	}
}

func TestRestoreRoundTripNewestSnapshot(t *testing.T) {
	h := newRestoreHarness(t)
	older, newest := goodSnapshot(t), goodSnapshot(t)
	putSnapshot(t, h.store, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), older, h.bundle)
	putSnapshot(t, h.store, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), newest, h.bundle)

	err := h.run()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(h.talos.uploaded, newest) {
		t.Fatal("Talos did not receive exactly the newest snapshot's bytes")
	}

	if !slices.Equal(h.talos.order, []string{"recover", "bootstrap"}) {
		t.Fatalf("call order = %v", h.talos.order)
	}

	h.assertNoLeak(t, err)
}

func TestRestoreNamedSnapshot(t *testing.T) {
	h := newRestoreHarness(t)
	older := goodSnapshot(t)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putSnapshot(t, h.store, t1, older, h.bundle)
	putSnapshot(t, h.store, t1.Add(24*time.Hour), goodSnapshot(t), h.bundle)

	h.opts.Snapshot = etcdbackup.ObjectName(t1)

	if err := h.run(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(h.talos.uploaded, older) {
		t.Fatal("named snapshot was not the one restored")
	}
}

func corruptSnapshot(t *testing.T) []byte {
	t.Helper()

	b := goodSnapshot(t)
	b[10] ^= 0xff // body no longer matches the trailer

	return b
}

func TestRestoreSkipsCorruptNewestButNeverSendsIt(t *testing.T) {
	h := newRestoreHarness(t)
	good := goodSnapshot(t)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putSnapshot(t, h.store, t1, good, h.bundle)
	putSnapshot(t, h.store, t1.Add(time.Hour), corruptSnapshot(t), h.bundle)

	if err := h.run(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(h.talos.uploaded, good) {
		t.Fatal("expected the older good snapshot")
	}

	if !strings.Contains(h.stderr.String(), "skipping") {
		t.Fatal("the skipped corrupt snapshot was not reported")
	}
}

func TestRestoreCorruptNamedSnapshotIsRefused(t *testing.T) {
	h := newRestoreHarness(t)
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	putSnapshot(t, h.store, t1, corruptSnapshot(t), h.bundle)
	putSnapshot(t, h.store, t1.Add(time.Hour), goodSnapshot(t), h.bundle)
	h.opts.Snapshot = etcdbackup.ObjectName(t1)

	err := h.run()
	if err == nil || len(h.talos.order) != 0 {
		t.Fatalf("expected refusal with nothing sent, err=%v calls=%v", err, h.talos.order)
	}

	h.assertNoLeak(t, err)
}

func TestRestoreWrongBundleCannotDecrypt(t *testing.T) {
	h := newRestoreHarness(t)
	other := newFreshTestBundle(t)
	putSnapshot(t, h.store, time.Now(), goodSnapshot(t), other)

	err := h.run()
	if err == nil || len(h.talos.order) != 0 {
		t.Fatalf("expected failure with nothing sent, err=%v calls=%v", err, h.talos.order)
	}

	h.assertNoLeak(t, err)
}

func TestRestoreDeclinedConfirmationSendsNothing(t *testing.T) {
	h := newRestoreHarness(t)
	putSnapshot(t, h.store, time.Now(), goodSnapshot(t), h.bundle)
	h.deps.AssumeYes = false
	h.deps.Stdin = strings.NewReader("n\n")

	if err := h.run(); err == nil || len(h.talos.order) != 0 {
		t.Fatalf("expected abort, err=%v calls=%v", err, h.talos.order)
	}

	h.deps.Stdin = strings.NewReader("y\n")

	if err := h.run(); err != nil || len(h.talos.order) != 2 {
		t.Fatalf("expected success on y, err=%v calls=%v", err, h.talos.order)
	}
}

func TestRestoreErrorsScrubCredentials(t *testing.T) {
	h := newRestoreHarness(t)
	h.store.listErr = errors.New("403 for " + testS3AccessKey + " / " + testS3SecretKey)

	err := h.run()
	if err == nil {
		t.Fatal("expected error")
	}

	h.assertNoLeak(t, err)

	h = newRestoreHarness(t)
	putSnapshot(t, h.store, time.Now(), goodSnapshot(t), h.bundle)
	h.talos.recErr = errors.New("upload failed " + testS3SecretKey)

	err = h.run()
	if err == nil || len(h.talos.order) != 1 {
		t.Fatalf("expected upload failure and no bootstrap, err=%v calls=%v", err, h.talos.order)
	}

	h.assertNoLeak(t, err)
}

func TestRestoreNeedsCredentialsFromEnvironment(t *testing.T) {
	h := newRestoreHarness(t)
	h.deps.Getenv = func(string) string { return "" }

	err := h.run()
	if err == nil || !strings.Contains(err.Error(), "ICHOR_S3_ACCESS_KEY_ID") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreNoSnapshots(t *testing.T) {
	h := newRestoreHarness(t)

	if err := h.run(); err == nil || len(h.talos.order) != 0 {
		t.Fatalf("expected error, err=%v", err)
	}
}
