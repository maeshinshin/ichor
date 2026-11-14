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
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

const uploadPassphrase = "current-passphrase"

// uploadTalos answers the recovery calls of one node: no etcd data yet, the snapshot is read in full by
// EtcdRecover (a read error stops it, as it would the real stream), and recoverErr is the node's answer
// to the upload.
type uploadTalos struct {
	TalosClient // any call not overridden here panics: the upload path makes only these

	recoverErr error
	bootstraps int
}

func (t *uploadTalos) EtcdDataPresent(context.Context) (bool, error) { return false, nil }

func (t *uploadTalos) EtcdRecover(_ context.Context, snapshot io.Reader) error {
	if _, err := io.Copy(io.Discard, snapshot); err != nil {
		return err
	}

	return recoveryError(t.recoverErr) // as machineryTalosClient maps the node's answer
}

func (t *uploadTalos) BootstrapRecover(context.Context) error {
	t.bootstraps++

	return nil
}

// pinnedStore serves one object. cut makes the download fail halfway, as a dropped connection does.
type pinnedStore struct {
	etcdbackup.Store // any call not overridden here panics

	object []byte
	cut    bool
}

func (s *pinnedStore) Get(context.Context, string) (io.ReadCloser, error) {
	if !s.cut {
		return io.NopCloser(bytes.NewReader(s.object)), nil
	}

	return io.NopCloser(io.MultiReader(bytes.NewReader(s.object[:len(s.object)/2]), failingReader{})), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

// uploadJob is a restoreJob whose target is talos and whose pinned snapshot is in store. The snapshot's
// own check (VerifyObject needs a bbolt file) is skipped: these tests are about the upload.
func uploadJob(talos TalosClient, store etcdbackup.Store, key etcdbackup.Key) *restoreJob {
	return &restoreJob{
		prefix:     "backups",
		store:      store,
		passphrase: key,
		target:     restoreNode{name: "cp-a", dial: func(context.Context) (TalosClient, error) { return talos, nil }},
		sent:       map[string]time.Time{},
		now:        time.Now,
		verified:   true,
	}
}

// sealedSnapshot is a 256 KiB snapshot sealed with key, so that the download spans several age chunks.
func sealedSnapshot(t *testing.T, key etcdbackup.Key) []byte {
	t.Helper()

	var buf bytes.Buffer

	w, err := etcdbackup.Encrypt(&buf, key)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write(bytes.Repeat([]byte("etcd"), 64<<10)); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// TestUploadWaitsWhenTheNodeIsUnavailable: a node that is rebooting answers Unavailable; that is a wait,
// not a failure of the restore. Guard: recoveryError's Unavailable case.
func TestUploadWaitsWhenTheNodeIsUnavailable(t *testing.T) {
	key := etcdbackup.Key{Current: uploadPassphrase}
	talos := &uploadTalos{recoverErr: status.Error(codes.Unavailable, "connection refused")}
	j := uploadJob(talos, &pinnedStore{object: sealedSnapshot(t, key)}, key)

	done, err := j.recoverTarget(context.Background())
	if err != nil {
		t.Fatalf("an Unavailable node is a wait, got a final error: %v", err)
	}

	if done || j.note == "" {
		t.Fatalf("want a wait with a note, got done=%v note=%q", done, j.note)
	}

	if talos.bootstraps != 0 {
		t.Fatal("bootstrapped after a refused upload")
	}
}

// TestUploadWaitsWhenTheDownloadFails: the snapshot's download breaks while it is uploaded. The node then
// reports the stream's error, which is not its verdict. Guard: readFailure, checked before the call error.
func TestUploadWaitsWhenTheDownloadFails(t *testing.T) {
	key := etcdbackup.Key{Current: uploadPassphrase}
	talos := &uploadTalos{}
	j := uploadJob(talos, &pinnedStore{object: sealedSnapshot(t, key), cut: true}, key)

	done, err := j.recoverTarget(context.Background())
	if err != nil {
		t.Fatalf("a failed download is a wait, got a final error: %v", err)
	}

	if done || !strings.Contains(j.note, "connection reset by peer") {
		t.Fatalf("want a wait naming the read error, got done=%v note=%q", done, j.note)
	}

	if talos.bootstraps != 0 {
		t.Fatal("bootstrapped from a truncated snapshot")
	}
}

// TestUploadStillFailsOnAFinalAnswer: any other status is still the node's final word, so the wait for
// Unavailable and for read errors does not swallow real refusals. Guards against over-broad mapping.
func TestUploadStillFailsOnAFinalAnswer(t *testing.T) {
	key := etcdbackup.Key{Current: uploadPassphrase}
	talos := &uploadTalos{recoverErr: status.Error(codes.Internal, "etcd refused the snapshot")}
	j := uploadJob(talos, &pinnedStore{object: sealedSnapshot(t, key)}, key)

	if _, err := j.recoverTarget(context.Background()); err == nil {
		t.Fatal("an Internal refusal must stay a final error")
	}
}
