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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestCacheDropsManagedFields: managedFields is often the largest part of a cached object and nothing in the
// controller reads it from the cache (hardware run I, the controller hit its memory limit).
func TestCacheDropsManagedFields(t *testing.T) {
	for _, ns := range []string{"", testIchorNamespace} {
		transform := CacheOptions(ns).DefaultTransform
		if transform == nil {
			t.Fatalf("CacheOptions(%q) has no DefaultTransform", ns)
		}

		node := &corev1.Node{}
		node.Name = "n1"
		node.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kubelet"}}

		out, err := transform(node)
		if err != nil {
			t.Fatal(err)
		}

		if got := out.(*corev1.Node).ManagedFields; len(got) != 0 {
			t.Errorf("CacheOptions(%q): managedFields kept: %v", ns, got)
		}
	}
}
