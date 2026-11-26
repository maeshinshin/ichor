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
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// +kubebuilder:rbac:groups="",resources=namespaces,resourceNames=ichor-system,verbs=get;patch

// ProtectNamespace labels the namespace the controller runs in with deleteguard.ProtectedLabel, so the
// ichor-protected-namespace policy refuses deleting it until the user consents. It writes metadata only, and
// nothing when the label is there. The rule that the controller never mutates spec is about custom
// resource specs, not Namespace metadata. reader must not be the cache: the role has no list or watch on
// Namespaces.
func ProtectNamespace(ctx context.Context, reader client.Reader, w client.Client, name string) error {
	var ns corev1.Namespace
	if err := reader.Get(ctx, client.ObjectKey{Name: name}, &ns); err != nil {
		return client.IgnoreNotFound(err)
	}

	if ns.Labels[deleteguard.ProtectedLabel] != "" {
		return nil
	}

	patch := client.MergeFrom(ns.DeepCopy())
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}

	ns.Labels[deleteguard.ProtectedLabel] = deleteguard.ProtectedValue

	return w.Patch(ctx, &ns, patch)
}

// ProtectNamespaceUntilDone calls ProtectNamespace until it succeeds or ctx ends. A failure is logged once per
// distinct message, not on every try (a permanent RBAC refusal would otherwise log every few seconds for the life of
// the pod; the logger is the context's), and the wait doubles from first up to limit.
func ProtectNamespaceUntilDone(
	ctx context.Context, reader client.Reader, w client.Client, name string, first, limit time.Duration,
) {
	log := logr.FromContextOrDiscard(ctx)
	var last string

	for wait := first; ; wait = min(2*wait, limit) {
		err := ProtectNamespace(ctx, reader, w, name)
		if err == nil {
			return
		}

		if msg := err.Error(); msg != last {
			last = msg

			log.Info("Could not label the namespace as protected yet; retrying with a growing wait", "error", msg, "retryAfter", wait.String())
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
