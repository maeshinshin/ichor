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
	"fmt"
	"io"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/internal/installmanifest"
)

const (
	// defaultCiliumWait bounds the wait for Cilium after everything else is up: its agent is
	// already running by then (the controller needed a Ready node), so this is a safety margin.
	defaultCiliumWait = 10 * time.Minute

	ciliumNamespace    = "kube-system"
	ciliumDaemonSet    = "cilium"
	ciliumPolicyCRDKey = "ciliumnetworkpolicies.cilium.io"
)

// applyNetworkPolicies applies Ichor's CiliumNetworkPolicies (ADR 0100) once Cilium is Ready:
// the cilium DaemonSet has an agent on every scheduled node and the CiliumNetworkPolicy CRD is
// Established. They cannot ride in the install manifest, which is applied before Cilium runs
// and so before their CRD exists. An empty manifest (a test) and any CNI but Cilium (nothing
// there enforces them, and the CRD never appears) skip it.
func applyNetworkPolicies(
	ctx context.Context, c client.Client, manifest []byte, cniPreset string, wait, interval time.Duration, progress io.Writer,
) error {
	if len(manifest) == 0 {
		return nil
	}

	if cniPreset != cniCilium {
		_, _ = fmt.Fprintf(progress, "init: not applying Ichor's network policies: --cni %s does not enforce CiliumNetworkPolicies\n", cniPreset)

		return nil
	}

	if bytes.Contains(manifest, []byte(installmanifest.PlaceholderSentinel)) {
		return fmt.Errorf("init: this ichor binary has no network policies embedded (built without -tags installembed)")
	}

	objs, err := installer.Decode(manifest)
	if err != nil {
		return fmt.Errorf("init: decoding Ichor's network policies: %w", err)
	}

	err = pollDiag(ctx, wait, interval, progress, "Cilium to be Ready", newWaitDiag(c, progress), func(ctx context.Context) error {
		return ciliumReady(ctx, c)
	}, func(error) bool { return true }, "Cilium to be Ready did not finish within "+wait.String())
	if err != nil {
		return fmt.Errorf("init: Cilium is not Ready, so Ichor's network policies were not applied: %w", err)
	}

	for _, obj := range objs {
		if err := createWithRetry(ctx, c, obj, progress); err != nil {
			return fmt.Errorf("init: applying Ichor's own %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	_, _ = fmt.Fprintf(progress, "init: applied Ichor's network policies (%d objects)\n", len(objs))

	return nil
}

// ciliumReady returns nil when the cilium DaemonSet has an agent Ready on every node it is
// scheduled to (and at least one) and its CiliumNetworkPolicy CRD is Established.
func ciliumReady(ctx context.Context, c client.Client) error {
	ds := &unstructured.Unstructured{}
	ds.SetAPIVersion("apps/v1")
	ds.SetKind("DaemonSet")

	if err := c.Get(ctx, types.NamespacedName{Namespace: ciliumNamespace, Name: ciliumDaemonSet}, ds); err != nil {
		return err
	}

	desired, _, _ := unstructured.NestedInt64(ds.Object, "status", "desiredNumberScheduled")
	ready, _, _ := unstructured.NestedInt64(ds.Object, "status", "numberReady")

	if desired == 0 || ready != desired {
		return fmt.Errorf("daemonset %s/%s has %d of %d agents ready", ciliumNamespace, ciliumDaemonSet, ready, desired)
	}

	crd := &unstructured.Unstructured{}
	crd.SetAPIVersion("apiextensions.k8s.io/v1")
	crd.SetKind("CustomResourceDefinition")

	if err := c.Get(ctx, types.NamespacedName{Name: ciliumPolicyCRDKey}, crd); err != nil {
		return err
	}

	conds, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
	for _, cond := range conds {
		if m, ok := cond.(map[string]any); ok && m["type"] == "Established" && m["status"] == "True" {
			return nil
		}
	}

	return fmt.Errorf("crd %s is not Established", ciliumPolicyCRDKey)
}
