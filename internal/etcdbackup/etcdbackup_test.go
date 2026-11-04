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
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// makeSnapshot builds a stream with etcd's snapshot shape: n*512 bytes of
// "database" minus the 32 trailing bytes, which are the sha256 of the rest.
func makeSnapshot(body string) []byte {
	db := []byte(body + strings.Repeat("x", 512-len(body)%512))
	sum := sha256.Sum256(db)

	return append(db, sum[:]...)
}

func TestVerifier(t *testing.T) {
	good := makeSnapshot("hello")

	v := NewVerifier()
	// Feed in awkward pieces: the trailer straddles writes.
	for i := 0; i < len(good); i += 7 {
		_, _ = v.Write(good[i:min(i+7, len(good))])
	}

	if err := v.Verify(); err != nil {
		t.Fatalf("good snapshot rejected: %v", err)
	}

	bad := bytes.Clone(good)
	bad[3] ^= 0xff

	v = NewVerifier()
	_, _ = v.Write(bad)

	if err := v.Verify(); err == nil {
		t.Fatal("corrupted snapshot accepted")
	}

	v = NewVerifier()
	_, _ = v.Write(good[:len(good)-512])

	if err := v.Verify(); err == nil {
		t.Fatal("truncated snapshot accepted")
	}
}

func testBundle(t *testing.T, clusterSecret string) *secrets.Bundle {
	t.Helper()

	return &secrets.Bundle{
		Cluster: &secrets.Cluster{ID: "cluster-id", Secret: clusterSecret},
		Secrets: &secrets.Secrets{SecretboxEncryptionSecret: "box-" + clusterSecret},
	}
}

func TestEncryptRoundTripAndKeyBinding(t *testing.T) {
	pass, err := DeriveKey(testBundle(t, "s3cr3t-one"))
	if err != nil {
		t.Fatal(err)
	}

	again, _ := DeriveKey(testBundle(t, "s3cr3t-one"))
	if pass != again {
		t.Fatal("passphrase derivation is not deterministic: a restore could not re-derive it")
	}

	other, _ := DeriveKey(testBundle(t, "s3cr3t-two"))
	if pass.Current == other.Current {
		t.Fatal("different bundles derived the same key")
	}

	plain := makeSnapshot("etcd data")
	enc := sealed(t, plain, pass)

	if bytes.Contains(enc, []byte("etcd data")) {
		t.Fatal("plaintext visible in ciphertext")
	}

	r, err := Decrypt(bytes.NewReader(enc), pass)
	if err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip failed: %v", err)
	}

	if _, err := Decrypt(bytes.NewReader(enc), other); err == nil {
		t.Fatal("decrypted with a key derived from a different bundle")
	}

	if _, err := DeriveKey(testBundle(t, "")); err == nil {
		t.Fatal("empty cluster secret accepted")
	}
}

func TestObjectNameRoundTrip(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	got, ok := ParseObjectName(ObjectName(at))
	if !ok || !got.Equal(at) {
		t.Fatalf("got %v %v", got, ok)
	}

	if _, ok := ParseObjectName("notes.txt"); ok {
		t.Fatal("foreign object parsed as a snapshot")
	}
}

func TestS3StoreAgainstFake(t *testing.T) {
	f := newFakeS3(t)

	st := f.store(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range 4 {
		body := []byte{byte(i)}
		key := "a/b/" + ObjectName(base.Add(time.Duration(i)*time.Hour))

		if err := st.Put(ctx, key, bytes.NewReader(body), 1); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	// A foreign object and a sibling prefix must never be listed or pruned.
	_ = st.Put(ctx, "a/b/notes.txt", strings.NewReader("x"), 1)
	_ = st.Put(ctx, "a/other/"+ObjectName(base), strings.NewReader("x"), 1)

	objs, err := st.List(ctx, "a/b")
	if err != nil || len(objs) != 4 {
		t.Fatalf("list: %v %v", objs, err)
	}

	if !objs[0].Time.After(objs[3].Time) {
		t.Fatal("list is not newest first")
	}

	if err := Prune(ctx, st, "a/b", 2); err != nil {
		t.Fatalf("prune: %v", err)
	}

	objs, _ = st.List(ctx, "a/b")
	if len(objs) != 2 || !objs[0].Time.Equal(base.Add(3*time.Hour)) {
		t.Fatalf("prune kept the wrong objects: %v", objs)
	}

	if _, ok := f.objects["bkt/a/b/notes.txt"]; !ok {
		t.Fatal("prune deleted a foreign object")
	}

	rc, err := st.Get(ctx, objs[0].Key)
	if err != nil {
		t.Fatal(err)
	}

	b, _ := io.ReadAll(rc)
	_ = rc.Close()

	if !bytes.Equal(b, []byte{3}) {
		t.Fatalf("get returned %v", b)
	}

	if _, err := st.Get(ctx, "a/b/"+ObjectName(base.Add(99*time.Hour))); err == nil {
		t.Fatal("get of a missing key succeeded")
	}
}

func TestNewS3StoreRejectsBadInput(t *testing.T) {
	if _, err := NewS3Store(S3Config{Endpoint: "s3.example.com"}); err == nil {
		t.Fatal("scheme-less endpoint accepted")
	}

	if _, err := NewS3Store(S3Config{Endpoint: "https://x.example", CAPEM: []byte("junk")}); err == nil {
		t.Fatal("garbage CA accepted")
	}
}

// A CA rotation replaces the bundle's certificates (talosctl rotate-ca consumes
// only Certs.OS and Certs.K8s of a fresh bundle, Talos v1.14.3
// cmd/talosctl/cmd/talos/rotate-ca.go) and leaves Cluster alone, so snapshots
// taken before it must stay decryptable after it.
func TestPassphraseSurvivesCARotation(t *testing.T) {
	before := testBundle(t, "s3cr3t-one")

	fresh, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), nil)
	if err != nil {
		t.Fatal(err)
	}

	after := testBundle(t, "s3cr3t-one")
	after.Certs = fresh.Certs

	a, _ := DeriveKey(before)

	b, _ := DeriveKey(after)
	if a != b {
		t.Fatal("rotating the CAs changed the backup key: older snapshots would no longer decrypt")
	}
}

// The v2 key comes from a control-plane-only secret, so the Cluster.Secret that every worker
// machine config carries no longer opens a backup, while a backup sealed before (v1) still opens.
func TestBackupKeyV2NeedsAControlPlaneSecret(t *testing.T) {
	b := testBundle(t, "cluster-secret")

	key, err := DeriveKey(b)
	if err != nil {
		t.Fatal(err)
	}

	if key.Current == key.Legacy {
		t.Fatal("v2 and v1 keys are equal")
	}

	plain := makeSnapshot("etcd data")

	seal := func(pass string) []byte { return sealed(t, plain, Key{Current: pass}) }

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

	if err := open(seal(key.Current), key); err != nil {
		t.Fatalf("v2 backup: %v", err)
	}

	if err := open(seal(key.Legacy), key); err != nil {
		t.Fatalf("v1 backup must stay readable: %v", err)
	}

	// An attacker with a worker's config knows the v1 key; a v2 backup must not open with it.
	if err := open(seal(key.Current), Key{Current: key.Legacy}); err == nil {
		t.Fatal("a v2 backup opened with the worker-derivable v1 key")
	}

	// A bundle without any control-plane-only secret cannot derive a key.
	noBox := testBundle(t, "cluster-secret")
	noBox.Secrets = &secrets.Secrets{}

	if _, err := DeriveKey(noBox); err == nil {
		t.Fatal("derived a backup key without a control-plane-only secret")
	}
}
