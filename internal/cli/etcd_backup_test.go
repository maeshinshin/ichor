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
	"strings"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestEtcdBackupNowSetsTheAnnotation(t *testing.T) {
	setAPITestFlags(t, true)

	bk := &ichorv1alpha1.TalosEtcdBackup{}
	bk.Name, bk.Namespace = "nightly", testNamespace
	bk.Spec.ClusterRef = corev1.LocalObjectReference{Name: opTestCluster}

	c, k8s := freshClients(t, append(opTestObjects(), bk))

	var out, errw bytes.Buffer

	if err := runEtcdBackupNow(context.Background(), c, &out, &errw, "nightly"); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.TalosEtcdBackup
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "nightly"}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Annotations[ichorv1alpha1.BackupNowAnnotation] == "" {
		t.Fatal("the backup-now annotation was not set")
	}

	if !strings.Contains(errw.String(), "requested a snapshot of nightly") {
		t.Fatalf("stderr = %q", errw.String())
	}

	// A second request while the first waits is refused.
	if err := runEtcdBackupNow(context.Background(), c, &out, &errw, "nightly"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second request: %v", err)
	}

	if err := runEtcdBackupNow(context.Background(), c, &out, &errw, "missing"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown backup: %v", err)
	}
}
