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

package storage

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	selRoleKey = "role"
	selRoleVal = "storage"
)

func selStorage(ns, name, cluster string, match map[string]string) ichorv1alpha1.TalosStorage {
	return ichorv1alpha1.TalosStorage{
		Namespace: ns, Name: name,
		Spec: ichorv1alpha1.TalosStorageSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster},
			Nodes:      metav1.LabelSelector{MatchLabels: match},
		},
	}
}

func TestSelect(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{Namespace: "ns", Name: "c1"}
	nodeLabels := map[string]string{selRoleKey: selRoleVal}
	storages := []ichorv1alpha1.TalosStorage{
		selStorage("ns", "b", "c1", map[string]string{selRoleKey: selRoleVal}),
		selStorage("ns", "a", "c1", map[string]string{selRoleKey: selRoleVal}),
		selStorage("ns", "other-cluster", "c2", map[string]string{selRoleKey: selRoleVal}),
		selStorage("other-ns", "other-ns", "c1", map[string]string{selRoleKey: selRoleVal}),
		selStorage("ns", "no-match", "c1", map[string]string{selRoleKey: "compute"}),
	}

	got, err := Select(cluster, storages, nodeLabels)
	if err != nil || got == nil || got.Name != "a" {
		t.Fatalf("Select = %v, %v; want the first match by name, \"a\"", got, err)
	}

	got, err = Select(cluster, storages, map[string]string{selRoleKey: "gpu"})
	if err != nil || got != nil {
		t.Fatalf("Select(non-matching labels) = %v, %v; want nil, nil", got, err)
	}

	bad := selStorage("ns", "bad", "c1", nil)
	bad.Spec.Nodes = metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "k", Operator: "bogus"},
	}}

	if _, err = Select(cluster, []ichorv1alpha1.TalosStorage{bad}, nodeLabels); err == nil {
		t.Fatal("Select with an invalid selector returned no error")
	}
}
