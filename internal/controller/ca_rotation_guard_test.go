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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The manager's Role has update on every Secret in its namespace (ADR 0063), so the executor itself must
// only ever write the Secret the TalosCluster's secretsRef names, in the controller namespace.
func TestCARotationWritesOnlyTheReferencedBundleSecret(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{Name: "c"}
	cluster.Spec.SecretsRef.Name = "bundle"

	secret := func(ns, name string) *corev1.Secret {
		return &corev1.Secret{Namespace: ns, Name: name}
	}

	cl := fake.NewClientBuilder().WithObjects(secret("ichor-system", "bundle"), secret("ichor-system", "other"),
		secret("elsewhere", "bundle")).Build()
	r := &TalosOperationReconciler{Client: cl, ControllerNamespace: "ichor-system"}

	for _, bad := range []*corev1.Secret{secret("ichor-system", "other"), secret("elsewhere", "bundle"), secret("ichor-system", "")} {
		if err := r.updateBundle(context.Background(), cluster, bad); err == nil {
			t.Errorf("wrote %s/%s, which is not the cluster's secrets bundle", bad.Namespace, bad.Name)
		}
	}

	if err := r.updateBundle(context.Background(), cluster, secret("ichor-system", "bundle")); err != nil {
		t.Errorf("refused the referenced bundle: %v", err)
	}
}
