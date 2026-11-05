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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CacheOptions confines the cached informers of namespaced kinds whose RBAC is granted only
// in the controller's own namespace (the Role in config/rbac/role.yaml) to that namespace:
// an unrestricted informer lists and watches cluster-wide, is forbidden, and its cache never
// syncs. Every namespaced kind read through the manager's cached client must be
// either granted cluster-wide on purpose or listed here; cache_rbac_test.go enforces that
// against the generated RBAC.
func CacheOptions(namespace string) cache.Options {
	// managedFields is often the biggest part of a cached object and nothing reads it from the cache.
	base := cache.Options{DefaultTransform: cache.TransformStripManagedFields()}

	if namespace == "" {
		return base
	}

	scoped := cache.ByObject{Namespaces: map[string]cache.Config{namespace: {}}}

	base.ByObject = map[client.Object]cache.ByObject{
		&corev1.Secret{}:    scoped,
		&corev1.ConfigMap{}: scoped,
		&corev1.Service{}:   scoped,
		// The installer Job runs in the controller's namespace, the Cilium Helm Job in
		// kube-system (cniJobNamespace, with its own Role: config/rbac/ichor-cni/controller_role.yaml).
		&batchv1.Job{}: {Namespaces: map[string]cache.Config{namespace: {}, cniJobNamespace: {}}},
	}

	return base
}
