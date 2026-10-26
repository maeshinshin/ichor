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
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// adoptDeps bundles adopt's side effects -- the Kubernetes API, each node's
// Talos API, and the terminal -- behind small interfaces/funcs, so
// runAdopt can be driven by a test with no real cluster, node or file on
// either end (unit rule 2's spirit, extended to the CLI).
type adoptDeps struct {
	K8s       client.Client
	Bundle    *secrets.Bundle
	DialTalos func(ctx context.Context, endpoint string) (talosConfigReader, error)

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	AssumeYes bool
	Output    string
}

// runAdopt implements `ichor adopt` (docs/design/architecture.md,
// "Bootstrap"): for every TalosMachine in cluster, it diffs the machine's
// live Talos configuration against a fresh render and turns the gap into a
// TalosConfigPatch (buildAdoptPlan does the actual diff/verify, this
// function is only orchestration -- fetch, confirm, create, report).
func runAdopt(ctx context.Context, clusterName, namespace string, deps adoptDeps) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := deps.K8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: clusterName}, &cluster); err != nil {
		return fmt.Errorf("adopt: getting TalosCluster %q: %w", clusterName, err)
	}

	// The controller's fixed namespace, not --namespace: that is where the controller reads
	// authSecretRef Secrets from, so the CLI resolves the same ones.
	sys, err := render.ResolveSystemInputs(ctx, deps.K8s, ichorControllerNamespace, &cluster)
	if err != nil {
		return fmt.Errorf("adopt: %w", err)
	}

	var machineList ichorv1alpha1.TalosMachineList
	if err := deps.K8s.List(ctx, &machineList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("adopt: listing TalosMachines: %w", err)
	}

	var patchList ichorv1alpha1.TalosConfigPatchList
	if err := deps.K8s.List(ctx, &patchList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("adopt: listing TalosConfigPatches: %w", err)
	}

	var existing []ichorv1alpha1.TalosConfigPatch

	for _, p := range patchList.Items {
		if p.Spec.ClusterRef.Name == clusterName {
			existing = append(existing, p)
		}
	}

	var plans []*adoptPlan

	for i := range machineList.Items {
		m := &machineList.Items[i]
		if m.Spec.ClusterRef.Name != clusterName {
			continue
		}

		plan, err := planMachine(ctx, &cluster, m, deps.Bundle, existing, sys, deps.DialTalos)
		if err != nil {
			return err
		}

		plans = append(plans, plan)
	}

	toCreate := reportPlans(deps.Stderr, plans)
	if len(toCreate) == 0 {
		_, _ = fmt.Fprintln(deps.Stderr, "adopt: nothing to adopt, every machine already matches a fresh render")

		return nil
	}

	patches := make([]ichorv1alpha1.TalosConfigPatch, 0, len(toCreate))
	for _, p := range toCreate {
		patches = append(patches, *p.Patch)
	}

	if err := writeOutput(deps.Stdout, deps.Output, patches); err != nil {
		return err
	}

	if !deps.AssumeYes {
		ok, err := confirm(deps.Stdin, deps.Stderr,
			fmt.Sprintf("Create %d TalosConfigPatch object(s) to adopt cluster %q with zero drift? [y/N]: ", len(patches), clusterName))
		if err != nil {
			return fmt.Errorf("adopt: reading confirmation: %w", err)
		}

		if !ok {
			return fmt.Errorf("adopt: aborted, nothing was created")
		}
	}

	for _, p := range toCreate {
		if err := deps.K8s.Create(ctx, p.Patch); err != nil {
			if apierrors.IsAlreadyExists(err) {
				_, _ = fmt.Fprintf(deps.Stderr, "adopt: TalosConfigPatch %q already exists, skipping\n", p.Patch.Name)

				continue
			}

			return fmt.Errorf("adopt: creating TalosConfigPatch for machine %q: %w", p.Machine, err)
		}

		_, _ = fmt.Fprintf(deps.Stderr, "adopt: created TalosConfigPatch %q for machine %q\n", p.Patch.Name, p.Machine)
	}

	return nil
}

// planMachine reads m's current configuration over the Talos API and
// builds its adoptPlan.
func planMachine(
	ctx context.Context,
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	existing []ichorv1alpha1.TalosConfigPatch,
	sys *render.SystemInputs,
	dial func(ctx context.Context, endpoint string) (talosConfigReader, error),
) (*adoptPlan, error) {
	talos, err := dial(ctx, m.Spec.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("adopt: dialing machine %q at %q: %w", m.Name, m.Spec.Endpoint, err)
	}

	current, err := talos.CurrentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("adopt: reading machine %q's current configuration: %w", m.Name, err)
	}

	return buildAdoptPlan(cluster, m, bundle, existing, current, sys)
}

// reportPlans writes a one-line-per-machine summary to w and returns the
// plans that have a patch ready to create.
func reportPlans(w io.Writer, plans []*adoptPlan) []*adoptPlan {
	var toCreate []*adoptPlan

	for _, p := range plans {
		switch {
		case p.Patch != nil:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: drift found, patch ready (zero drift verified)\n", p.Machine)

			toCreate = append(toCreate, p)
		case len(p.UnrepresentablePaths) > 0:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: drift found in %d field(s) that adopt cannot patch automatically: %s\n",
				p.Machine, len(p.UnrepresentablePaths), strings.Join(p.UnrepresentablePaths, ", "))
			_, _ = fmt.Fprintf(w, "adopt: machine %q: write a TalosConfigPatch for these fields by hand, then re-run adopt\n", p.Machine)
		default:
			_, _ = fmt.Fprintf(w, "adopt: machine %q: already matches a fresh render, nothing to do\n", p.Machine)
		}
	}

	return toCreate
}

// confirm reads a yes/no answer from r, defaulting to no on anything but a
// leading 'y' or 'Y' (architecture.md, "CLI and UI": interactive
// confirmation with --yes to skip it).
func confirm(r io.Reader, w io.Writer, prompt string) (bool, error) {
	_, _ = fmt.Fprint(w, prompt)

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}

	line = strings.TrimSpace(line)

	return line == "y" || line == "Y" || line == "yes" || line == "Yes", nil
}
