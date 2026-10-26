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
	"fmt"
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// planText is the confirmation a destructive operation asks: what changes,
// how many machines, in which order, and what happens to each one. It is a
// pure function of the server's plan (OperationService.PlanOperation), so the
// CLI and the UI say the same thing about the same plan (docs/adr/0068).
func planText(kind pb.OperationKind, cluster string, p *pb.PlanOperationResponse) string {
	var b strings.Builder

	b.WriteString(headline(kind, cluster, p))
	b.WriteString("\n")

	switch p.GetEffect() {
	case pb.OperationEffect_OPERATION_EFFECT_REBOOT:
		b.WriteString("Each machine is upgraded and REBOOTS.\n")
	case pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS:
		b.WriteString("Configuration is applied without a reboot; the kubelet and control plane components restart on each machine.\n")
	case pb.OperationEffect_OPERATION_EFFECT_STOP:
		b.WriteString("Every machine is POWERED OFF and stays off until you start it again. The Kubernetes API goes down.\n")
	case pb.OperationEffect_OPERATION_EFFECT_NONE:
		b.WriteString("No machine is touched; a Job applies the new install manifest.\n")
	case pb.OperationEffect_OPERATION_EFFECT_CA_ROTATION:
		fmt.Fprintf(&b, "Every machine is reconfigured once per pass (%s), without a reboot. "+
			"The kubelet RESTARTS on each machine in the passes that change the Kubernetes CA.\n", strings.Join(p.GetStages(), ", "))
	case pb.OperationEffect_OPERATION_EFFECT_UNSPECIFIED:
	}

	if ms := p.GetMachines(); len(ms) > 0 {
		b.WriteString(orderText(kind, p))
	} else if p.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_NONE {
		b.WriteString("No approved machine is selected, so nothing would be changed.\n")
	}

	if h := p.GetHeld(); len(h) > 0 {
		fmt.Fprintf(&b, "Held (skipped, upgrade-hold annotation): %s\n", strings.Join(h, ", "))
	}

	if p.GetReplacesSecretsBundle() {
		fmt.Fprintf(&b, "When it completes, the secrets bundle in the cluster is replaced and the copy on your machine is stale: "+
			"run 'ichor cluster bundle-export %s' and keep the file safe.\n", cluster)
	}

	if kind != pb.OperationKind_OPERATION_KIND_SHUTDOWN {
		b.WriteString("A machine that fails pauses the operation; nothing is rolled back.\n")
	}

	return b.String()
}

func headline(kind pb.OperationKind, cluster string, p *pb.PlanOperationResponse) string {
	to := p.GetTarget()
	if to == "" || to == p.GetCurrent() {
		to = fmt.Sprintf("%s (what its spec already says)", p.GetCurrent())
	}

	switch kind {
	case pb.OperationKind_OPERATION_KIND_TALOS:
		return fmt.Sprintf("Upgrade Talos on cluster %q from %s to %s.", cluster, p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_KUBERNETES:
		return fmt.Sprintf("Upgrade Kubernetes on cluster %q from %s to %s.", cluster, p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_ICHOR:
		return fmt.Sprintf("Upgrade Ichor itself from %s to %s.", p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_CNI:
		return fmt.Sprintf("Switch kube-proxy on cluster %q from %s to %s.", cluster, p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_CA_ROTATION:
		return fmt.Sprintf("Rotate the Talos API and Kubernetes API certificate authorities (ca-rotation) of cluster %q.", cluster)
	case pb.OperationKind_OPERATION_KIND_SHUTDOWN:
		return fmt.Sprintf("SHUT DOWN cluster %q.", cluster)
	case pb.OperationKind_OPERATION_KIND_UNSPECIFIED:
	}

	return fmt.Sprintf("Start an operation on cluster %q.", cluster)
}

// orderText lists the machines step by step; machines sharing a step go together.
func orderText(kind pb.OperationKind, p *pb.PlanOperationResponse) string {
	var (
		b     strings.Builder
		cps   int
		roles []string
		names = map[int32][]string{}
		order []int32
	)

	for _, m := range p.GetMachines() {
		if m.GetRole() == roleControlPlane {
			cps++
		}

		if _, ok := names[m.GetStep()]; !ok {
			order = append(order, m.GetStep())
			roles = append(roles, m.GetRole())
		}

		names[m.GetStep()] = append(names[m.GetStep()], m.GetName())
	}

	steps := make([]string, 0, len(order))

	for i, s := range order {
		steps = append(steps, fmt.Sprintf("  %d. %s (%s)", i+1, strings.Join(names[s], ", "), roles[i]))
	}

	total := len(p.GetMachines())
	fmt.Fprintf(&b, "%d machine(s) (%d control plane, %d worker)", total, cps, total-cps)

	switch kind {
	case pb.OperationKind_OPERATION_KIND_SHUTDOWN:
		b.WriteString(": workers first, drained and stopped together, then the control planes at once:\n")
	case pb.OperationKind_OPERATION_KIND_CA_ROTATION:
		b.WriteString(", one at a time, control planes first; every machine is visited in each pass:\n")
	default:
		fmt.Fprintf(&b, ": control planes one at a time, then workers (at most %d at once), in this order:\n", max(p.GetMaxUnavailable(), 1))
	}

	b.WriteString(strings.Join(steps, "\n"))
	b.WriteString("\n")

	return b.String()
}
