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
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

const wipingStep = "Wiping"

// TestRestoreObjectsAgainstFakeS3 runs the marker and the pinned copy of an etcd restore (ADR 0089)
// through the real S3 client: a missing marker is "none", not an error, and neither object is ever
// taken for a snapshot by List or Prune.
func TestRestoreObjectsAgainstFakeS3(t *testing.T) {
	f := newFakeS3()
	defer f.srv.Close()

	st, err := NewS3Store(S3Config{
		Endpoint: f.srv.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "AK", SecretAccessKey: "SK", CAPEM: f.caPEM(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	if m, err := GetMarker(ctx, st, "p/c", "pw"); err != nil || m != nil {
		t.Fatalf("no marker: got %v, %v", m, err)
	}

	if _, err := st.Get(ctx, "p/c/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing key must be ErrNotFound, got %v", err)
	}

	want := &RestoreMarker{
		Namespace: "ns", Operation: "etcd-restore-x", Cluster: "c", Backup: "nightly", Snapshot: ObjectName(time.Unix(0, 0)),
		Reason: "bad change", SnapshotTime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Target: "cp-1",
		Step: wipingStep, StartedAt: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		Nodes: map[string]string{"cp-2": "node-2"}, Own: "cp-2",
	}

	if err := PutMarker(ctx, st, "p/c", "pw", want); err != nil {
		t.Fatal(err)
	}

	got, err := GetMarker(ctx, st, "p/c", "pw")
	if err != nil || got == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("marker round trip: %+v, %v", got, err)
	}

	snap := makeSnapshot("data")
	key := "p/c/" + ObjectName(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	if err := st.Put(ctx, key, bytes.NewReader(snap), int64(len(snap))); err != nil {
		t.Fatal(err)
	}

	// What Pin writes (its own copy is checked below, against memStore: the fake S3 holds a lock across
	// a request and cannot serve a download and an upload that overlap).
	if err := st.Put(ctx, PinnedKey("p/c"), bytes.NewReader(snap), int64(len(snap))); err != nil {
		t.Fatal(err)
	}

	objs, err := st.List(ctx, "p/c")
	if err != nil || len(objs) != 1 || objs[0].Key != key {
		t.Fatalf("List must show the snapshot only, got %v, %v", objs, err)
	}

	if err := Prune(ctx, st, "p/c", 1); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if err := DeleteMarker(ctx, st, "p/c"); err != nil {
		t.Fatal(err)
	}

	if m, err := GetMarker(ctx, st, "p/c", "pw"); err != nil || m != nil {
		t.Fatalf("after delete: %v, %v", m, err)
	}
}

func TestPinCopiesTheCiphertext(t *testing.T) {
	st := memStore{"p/c/" + ObjectName(time.Unix(0, 0)): sealed(t, makeSnapshot("etcd"), Key{Current: "pw"})}

	if err := Pin(context.Background(), st, "p/c", "p/c/"+ObjectName(time.Unix(0, 0)), 0); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(st[PinnedKey("p/c")], st["p/c/"+ObjectName(time.Unix(0, 0))]) {
		t.Fatal("the pinned copy is not the stored ciphertext")
	}

	if err := Pin(context.Background(), st, "p/c", "p/c/missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pinning a missing snapshot: %v", err)
	}
}

// memStore is the smallest Store for the verification tests.
type memStore map[string][]byte

func (m memStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	m[key] = b

	return err
}

func (m memStore) List(context.Context, string) ([]Object, error) { return nil, nil }

func (m memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m[key]
	if !ok {
		return nil, ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m memStore) Delete(_ context.Context, key string) error { delete(m, key); return nil }

func sealed(t *testing.T, plain []byte, pass Key) []byte {
	t.Helper()

	var buf bytes.Buffer

	w, err := Encrypt(&buf, pass)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = w.Write(plain)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestFetchVerifiedVerifyObjectAndReader(t *testing.T) {
	ctx := context.Background()
	plain := makeSnapshot("etcd")
	bad := append([]byte(nil), plain...)
	bad[1] ^= 0xff

	st := memStore{"good": sealed(t, plain, Key{Current: "pw"}), "bad": sealed(t, bad, Key{Current: "pw"})}

	buf, err := FetchVerified(ctx, st, "good", Key{Current: "pw"})
	if err != nil || !bytes.Equal(buf.Bytes(), plain) {
		t.Fatalf("FetchVerified: %v", err)
	}

	if n, err := VerifyObject(ctx, st, "good", Key{Current: "pw"}); err != nil || n != int64(len(plain)) {
		t.Fatalf("VerifyObject: %d, %v", n, err)
	}

	if _, err := VerifyObject(ctx, st, "bad", Key{Current: "pw"}); err == nil {
		t.Fatal("a corrupt snapshot passed")
	}

	if _, err := FetchVerified(ctx, st, "good", Key{Current: "other"}); err == nil {
		t.Fatal("a snapshot opened with the wrong key")
	}

	if _, err := VerifyObject(ctx, st, "nope", Key{Current: "pw"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing snapshot: %v", err)
	}

	rd, err := Reader(ctx, st, "good", Key{Current: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := io.ReadAll(rd)
	_ = rd.Close()

	if !bytes.Equal(got, plain) {
		t.Fatal("Reader returned different bytes")
	}

	if _, err := Reader(ctx, st, "good", Key{Current: "other"}); err == nil {
		t.Fatal("Reader opened with the wrong key")
	}
}

func TestMarkerSignature(t *testing.T) {
	ctx := context.Background()
	st := memStore{}
	m := &RestoreMarker{Namespace: "ns", Step: wipingStep}

	if err := PutMarker(ctx, st, "p", "pw", m); err != nil {
		t.Fatal(err)
	}

	if _, err := GetMarker(ctx, st, "p", "other"); err == nil {
		t.Fatal("a marker read with another key must be refused")
	}

	forged := bytes.Replace(st["p/"+MarkerName], []byte(wipingStep), []byte("Rejoin"), 1)
	st["p/"+MarkerName] = forged

	if _, err := GetMarker(ctx, st, "p", "pw"); err == nil {
		t.Fatal("an edited marker must be refused")
	}

	// A legacy unsigned marker is accepted (an upgrade must not strand a restore in flight).
	legacy, _ := json.Marshal(m)
	st["p/"+MarkerName] = legacy

	got, err := GetMarker(ctx, st, "p", "pw")
	if err != nil || got == nil || got.Step != wipingStep {
		t.Fatalf("legacy marker: %+v, %v", got, err)
	}
}
