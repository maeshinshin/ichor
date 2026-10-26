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

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestRotateCAWritesOneOperationAfterConfirmation(t *testing.T) {
	cluster := newTestCluster()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()

	old := assumeYes
	t.Cleanup(func() { assumeYes = old })

	assumeYes = false

	// Declined: nothing is written.
	if err := runRotateCA(context.Background(), k8s, strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{}, testNamespace, cluster.Name); err == nil {
		t.Fatal("a declined confirmation must abort")
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := k8s.List(context.Background(), &ops); err != nil || len(ops.Items) != 0 {
		t.Fatalf("a declined rotation wrote something: %v %v", err, ops.Items)
	}

	if err := runRotateCA(context.Background(), k8s, strings.NewReader("y\n"), &bytes.Buffer{}, &bytes.Buffer{}, testNamespace, cluster.Name); err != nil {
		t.Fatal(err)
	}

	if err := k8s.List(context.Background(), &ops); err != nil || len(ops.Items) != 1 {
		t.Fatalf("want one operation, got %v %v", err, ops.Items)
	}

	if got := ops.Items[0].Spec; got.Kind != ichorv1alpha1.TalosOperationKindCARotation || got.ClusterRef.Name != cluster.Name {
		t.Fatalf("wrong operation: %+v", got)
	}

	// A second rotation while the first has not completed is refused.
	assumeYes = true
	if err := runRotateCA(context.Background(), k8s, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, testNamespace, cluster.Name); err == nil {
		t.Fatal("a second rotation was started while one is running")
	}
}
