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
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Golden tests of the human output of `ichor snapshot etcd`, on a pipe: no colour codes.

func TestDownloadHumanOutput(t *testing.T) {
	f := &fakeDiagnostics{chunks: []*pb.DownloadEtcdSnapshotResponse{{Filename: "etcd.snapshot.db", Data: []byte("abc")}, {Data: []byte("def")}}}
	out := filepath.Join(t.TempDir(), "snap.db")

	var stderr bytes.Buffer
	if err := runDownload(context.Background(), diagClients(t, f, false), out, nil, &stderr, call); err != nil {
		t.Fatal(err)
	}

	want := "✓ Download — 6 B to " + out + " (mode 0600)\n" +
		"! the file is NOT encrypted and may contain secrets; keep it safe\n"
	wantText(t, stderr.String(), want)
}

func TestDownloadFailureIsShownOnceWithTheHint(t *testing.T) {
	var stderr bytes.Buffer

	err := runDownload(context.Background(), diagClients(t, &fakeDiagnostics{deny: true}, false), filepath.Join(t.TempDir(), "x"), nil, &stderr, call)
	if connect.CodeOf(err) != connect.CodePermissionDenied || !errorShown(err) {
		t.Fatalf("err = %v (shown %v)", err, errorShown(err))
	}

	if !strings.HasPrefix(stderr.String(), "✗ Download — failed\n") || !strings.Contains(stderr.String(), "run again with --local") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func etcdBackupEnv(t *testing.T) *apiClients {
	t.Helper()

	bk := &ichorv1alpha1.TalosEtcdBackup{}
	bk.Name, bk.Namespace = liveBackup, testNamespace
	bk.Spec.ClusterRef = corev1.LocalObjectReference{Name: opTestCluster}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(opTestObjects(), bk)...).Build()

	return localClients(apiOpts, apiDeps{K8s: k8s})
}

func TestEtcdBackupNowHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out, errw bytes.Buffer
	if err := runEtcdBackupNow(context.Background(), etcdBackupEnv(t), &out, &errw, liveBackup); err != nil {
		t.Fatal(err)
	}

	if want := "✓ requested a snapshot of backup nightly · " + testNamespace + "; the controller takes it shortly\n"; out.String() != want || errw.Len() != 0 {
		t.Errorf("stdout %q, stderr %q, want %q", out.String(), errw.String(), want)
	}
}

func TestEtcdBackupNowMachineOutputIsUnchanged(t *testing.T) {
	setHumanTestFlags(t, outputYAML)

	var out, errw bytes.Buffer
	if err := runEtcdBackupNow(context.Background(), etcdBackupEnv(t), &out, &errw, liveBackup); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(out.String(), "audit:") && !strings.HasPrefix(out.String(), "backup:") || strings.Contains(out.String(), "✓") {
		t.Errorf("stdout = %q", out.String())
	}

	if want := "ichor: requested a snapshot of nightly; the controller takes it shortly\n"; errw.String() != want {
		t.Errorf("stderr = %q, want %q", errw.String(), want)
	}
}
