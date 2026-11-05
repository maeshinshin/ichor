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
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	defaultReleaseWait     = 10 * time.Minute
	defaultReleaseInterval = 2 * time.Second

	conditionReady        = "Ready"
	conditionAdoptionHeld = "AdoptionHeld"
)

// caRotationAdvice is ADR 0103 1.4's recommendation, word for word. Advice only: nothing here
// starts a rotation.
const caRotationAdvice = "the Talos and Kubernetes client certificates of the previous administrators, and the CA keys held by " +
	"whoever created this cluster, remain valid; consider `ichor cluster rotate-ca`"

// releaseOrder puts control planes first, one machine after the other, then workers; each group by
// name so a rerun releases in the same order.
func releaseOrder(nodes []*adoptNode) []*adoptNode {
	out := slices.Clone(nodes)

	slices.SortStableFunc(out, func(a, b *adoptNode) int {
		if a.controlPlane() != b.controlPlane() {
			if a.controlPlane() {
				return -1
			}

			return 1
		}

		return strings.Compare(a.M.Name, b.M.Name)
	})

	return out
}

// releaseHold is phase 4 of ADR 0103 1.8. For each node, in order, it re-reads the live
// configuration, requires zero class A and class C drift against a render of what is now in the
// API (class B was consented to), removes the hold-apply annotation, and waits for the machine to
// report Ready with no AdoptionHeld before it goes on to the next. The controller serialises the
// control planes under the cluster lock; adopt only keeps the order. A machine that still drifts
// stops the release with its hold, and the ones after it, in place.
func releaseHold(
	ctx context.Context,
	deps adoptDeps,
	clusterName, namespace string,
	nodes []*adoptNode,
	bundle *secrets.Bundle,
	sys *render.SystemInputs,
	own func(*adoptNode) ownership,
) (int, error) {
	released := 0

	for _, n := range releaseOrder(nodes) {
		key := types.NamespacedName{Namespace: namespace, Name: n.M.Name}

		var m ichorv1alpha1.TalosMachine
		if err := deps.K8s.Get(ctx, key, &m); err != nil {
			return released, fmt.Errorf("adopt: getting TalosMachine %q: %w", n.M.Name, err)
		}

		if m.Annotations[ichorv1alpha1.TalosMachineHoldApplyAnnotation] != annotationTrue {
			continue
		}

		var cluster ichorv1alpha1.TalosCluster
		if err := deps.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: clusterName}, &cluster); err != nil {
			return released, fmt.Errorf("adopt: getting TalosCluster %q: %w", clusterName, err)
		}

		patches, err := clusterPatches(ctx, deps.K8s, namespace, clusterName)
		if err != nil {
			return released, err
		}

		talos, err := deps.DialTalos(ctx, m.DialEndpoint())
		if err != nil {
			return released, fmt.Errorf("adopt: dialing machine %q at %q: %w", m.Name, m.DialEndpoint(), err)
		}

		live, err := talos.CurrentConfig(ctx)
		if err != nil {
			return released, fmt.Errorf("adopt: reading machine %q's live configuration: %w", m.Name, err)
		}

		plan, err := buildAdoptPlan(&cluster, &m, bundle, patches, live, sys, own(n))
		if err != nil {
			return released, err
		}

		if plan.Patch != nil || len(plan.UnrepresentablePaths) > 0 {
			return released, fmt.Errorf("adopt: machine %q still drifts outside what Ichor owns (%s); its hold stays, and so does the hold of every machine after it",
				m.Name, strings.Join(append(append([]string{}, plan.UnrepresentablePaths...), driftKinds(plan)...), ", "))
		}

		before := m.DeepCopy()
		delete(m.Annotations, ichorv1alpha1.TalosMachineHoldApplyAnnotation)

		if err := deps.K8s.Patch(ctx, &m, client.MergeFrom(before)); err != nil {
			return released, fmt.Errorf("adopt: releasing the hold on machine %q: %w", m.Name, err)
		}

		_, _ = fmt.Fprintf(deps.Stderr, "adopt: released the hold on machine %q\n", m.Name)

		released++

		if err := waitMachineReleased(ctx, deps, key); err != nil {
			return released, err
		}
	}

	return released, nil
}

// driftKinds names a patch-able drift for the refusal message.
func driftKinds(p *adoptPlan) []string {
	if p.Patch == nil {
		return nil
	}

	return []string{"representable drift not yet captured"}
}

func clusterPatches(ctx context.Context, c client.Client, namespace, clusterName string) ([]ichorv1alpha1.TalosConfigPatch, error) {
	var list ichorv1alpha1.TalosConfigPatchList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("adopt: listing TalosConfigPatches: %w", err)
	}

	var out []ichorv1alpha1.TalosConfigPatch

	for _, p := range list.Items {
		if p.Spec.ClusterRef.Name == clusterName {
			out = append(out, p)
		}
	}

	return out, nil
}

// waitMachineReleased polls until the machine is Ready and no longer held.
func waitMachineReleased(ctx context.Context, deps adoptDeps, key types.NamespacedName) error {
	wait := cmp.Or(deps.ReleaseWait, defaultReleaseWait)
	interval := cmp.Or(deps.Interval, defaultReleaseInterval)

	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	for {
		var m ichorv1alpha1.TalosMachine

		err := deps.K8s.Get(ctx, key, &m)
		if err == nil && apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionReady) &&
			!apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionAdoptionHeld) {
			return nil
		}

		if sleepCtx(ctx, interval) != nil {
			return fmt.Errorf("adopt: machine %q did not become Ready within %s after its hold was released; "+
				"the machines after it stay held", key.Name, wait)
		}
	}
}
