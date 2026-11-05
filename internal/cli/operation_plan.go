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
	"fmt"
	"slices"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// credentialsStopWorking is what a CA rotation does to the operator's credentials, in the words the
// plan and rotate-ca's help share (the UI's copy says the same in its own strings). The nodes start
// serving certificates from the new CA at Issuing, the second pass, so a kubeconfig or talosconfig
// that trusts only the old CA fails from there on.
const credentialsStopWorking = "kubeconfig and talosconfig STOP WORKING from the second pass (Issuing) on: " +
	"the nodes start serving certificates from the new CA, and a file that trusts only the old CA can no longer verify them."

// caPlanHas reports whether the plan's CA targets include name; a plan from a server that does not say
// (empty) is the default pair, talos and kubernetes.
func caPlanHas(targets []string, name string) bool {
	if len(targets) == 0 {
		targets = ichorv1alpha1.DefaultCARotationTargets()
	}

	return slices.Contains(targets, name)
}

// caPlanNames names the CAs of a ca-rotation plan in words: "Talos API and Kubernetes API".
func caPlanNames(targets []string) string {
	var names []string

	// etcd is its own name.
	words := map[string]string{
		ichorv1alpha1.CARotationTargetTalos: "Talos API", ichorv1alpha1.CARotationTargetKubernetes: "Kubernetes API",
		ichorv1alpha1.CARotationTargetAggregator: "Kubernetes aggregator",
	}

	for _, target := range []string{
		ichorv1alpha1.CARotationTargetTalos, ichorv1alpha1.CARotationTargetKubernetes,
		ichorv1alpha1.CARotationTargetEtcd, ichorv1alpha1.CARotationTargetAggregator,
	} {
		if caPlanHas(targets, target) {
			names = append(names, cmp.Or(words[target], target))
		}
	}

	if len(names) < 2 {
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// keyRotationText is what a key rotation does, in the words the plan and rotate-keys' help share (the UI's
// copy says the same in its own strings).
func keyRotationText(stages []string) string {
	return fmt.Sprintf("Every control plane is reconfigured once per applying stage (%s), one at a time, without a reboot: its "+
		"kube-apiserver RESTARTS each time and Ichor waits for it to answer before the next control plane. Workers carry neither key and are not touched.\n"+
		"The new keys are first only accepted everywhere (the service-account public key verifies, the secretbox key decrypts), then made "+
		"primary. With the secretbox key, every Secret in the cluster is then rewritten under the new key by the controller (through an "+
		"admin kubeconfig a control plane issues, so Ichor needs no new permission) until a sweep finds none left. With the service-account "+
		"key, the old one is kept for 90 minutes by default (--token-settle) while pods' tokens are re-issued, and until no legacy "+
		"token Secret (type kubernetes.io/service-account-token) is signed by it (--ignore-legacy-tokens skips that check); tokens kept "+
		"outside the cluster stop working when it is dropped.\n"+
		"Backups sealed before the rotation stay readable (the bundle keeps the retired keys), but a snapshot taken before the "+
		"rotation finished cannot be restored.\n", strings.Join(stages, ", "))
}

// configRestartText is what a config operation whose changes need no reboot does (ADR 0080
// amendment); the UI says the same in ops.plan.effectConfigRestart.
const configRestartText = "Restarts the Kubernetes API server on each control plane, one at a time; no reboot."

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
		if kind == pb.OperationKind_OPERATION_KIND_CONFIG {
			b.WriteString("Each machine below gets its rendered configuration staged and REBOOTS (Ichor cordons and drains it first, since Talos does not on a reboot).\n")
		} else {
			b.WriteString("Each machine is upgraded and REBOOTS.\n")
		}
	case pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS:
		if kind == pb.OperationKind_OPERATION_KIND_CONFIG {
			b.WriteString(configRestartText + "\n")

			break
		}

		b.WriteString("Configuration is applied without a reboot; the kubelet and control plane components restart on each machine.\n")
		if kind == pb.OperationKind_OPERATION_KIND_KUBERNETES && len(p.GetStages()) > 0 {
			fmt.Fprintf(&b, "It runs in %d stages, each finished on every machine before the next starts, "+
				"so nothing is ever newer than an API server:\n", len(p.GetStages()))

			for i, s := range p.GetStages() {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, s)
			}
		}
	case pb.OperationEffect_OPERATION_EFFECT_STOP:
		b.WriteString("Every machine is POWERED OFF and stays off until you start it again. The Kubernetes API goes down.\n")
	case pb.OperationEffect_OPERATION_EFFECT_NONE:
		b.WriteString("No machine is touched; a Job applies the new install manifest.\n")
	case pb.OperationEffect_OPERATION_EFFECT_CA_ROTATION:
		targets := p.GetCaTargets()
		kube := caPlanHas(targets, ichorv1alpha1.CARotationTargetKubernetes)
		credentials := kube || caPlanHas(targets, ichorv1alpha1.CARotationTargetTalos)

		if caPlanHas(targets, ichorv1alpha1.CARotationTargetEtcd) {
			// Talos does not restart etcd on a configuration change, so the control planes reboot (ADR 0120).
			fmt.Fprintf(&b, "Every machine is reconfigured once per pass (%s); the control planes REBOOT in each pass (see the warning below), workers do not.",
				strings.Join(p.GetStages(), ", "))
		} else {
			fmt.Fprintf(&b, "Every machine is reconfigured once per pass (%s), without a reboot.", strings.Join(p.GetStages(), ", "))
		}

		if kube {
			b.WriteString(" The kubelet RESTARTS on each machine in the passes that change the Kubernetes CA.")
		}

		b.WriteString("\n")

		if credentials {
			b.WriteString("YOUR " + credentialsStopWorking + "\n" +
				"So after every machine accepts the new CAs the operation WAITS, changing nothing, until you have new credentials. " +
				"'ichor cluster rotate-ca' writes them (a kubeconfig and a talosconfig trusting the old and the new CAs, next to your current ones, " +
				"mode 0600, never printed) while your current ones still work, checks them, and only then lets the operation go on. " +
				"To continue, run 'ichor cluster credentials NAME --acknowledge' with your current kubeconfig, or, if you may " +
				"(the 'download' verb on talosclusters/credentials), use the operation page of the UI: it downloads the same files and continues the rotation.\n")
		} else {
			b.WriteString("Nothing your kubeconfig or talosconfig depends on changes, so there is no credential hand-off.\n")
		}

		if kube {
			b.WriteString("Pods change from the old CA to the new one at the second pass (Issuing): the API server's certificate changes at once, " +
				"but a pod's copy of the CA (kube-root-ca.crt) is refreshed by the kubelet within about a minute, so for that time in-cluster clients " +
				"cannot verify the API server. Ichor waits until every namespace has the new CA and then two minutes before the old CAs are dropped. " +
				"CoreDNS, kube-proxy, the CNI and ichor-ui are restarted once after the old CAs are dropped; other in-cluster clients that load the CA once may need a restart.\n")
		}
	case pb.OperationEffect_OPERATION_EFFECT_KEY_ROTATION:
		b.WriteString(keyRotationText(p.GetStages()))
	case pb.OperationEffect_OPERATION_EFFECT_UNSPECIFIED:
	}

	if ms := p.GetMachines(); len(ms) > 0 {
		b.WriteString(orderText(kind, p))
	} else if p.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_NONE {
		b.WriteString("No approved machine is selected, so nothing would be changed.\n")
	}

	if p.GetKubernetesApiUnavailable() {
		b.WriteString("This cluster has one control plane: the Kubernetes API is UNAVAILABLE while it reboots or restarts its API server.\n")
	}

	if w := p.GetWarning(); w != "" {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}

	if h := p.GetHeld(); len(h) > 0 {
		fmt.Fprintf(&b, "Held (skipped, upgrade-hold annotation): %s\n", strings.Join(h, ", "))
	}

	if p.GetReplacesSecretsBundle() {
		fmt.Fprintf(&b, "When it completes, the secrets bundle in the cluster is replaced and the copy on your machine is stale: "+
			"run 'ichor cluster bundle-export %s' and keep the file safe.\n", cluster)
	}

	if kind != pb.OperationKind_OPERATION_KIND_SHUTDOWN && p.GetEffect() != pb.OperationEffect_OPERATION_EFFECT_NONE {
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
		if p.GetTargetSchematic() != "" && (p.GetTarget() == "" || p.GetTarget() == p.GetCurrent()) {
			return fmt.Sprintf("Upgrade Talos on cluster %q, keeping %s: schematic %s → %s.",
				cluster, p.GetCurrent(), p.GetCurrentSchematic(), p.GetTargetSchematic())
		}

		if p.GetTargetSchematic() != "" {
			return fmt.Sprintf("Upgrade Talos on cluster %q from %s to %s, and its schematic %s → %s.",
				cluster, p.GetCurrent(), to, p.GetCurrentSchematic(), p.GetTargetSchematic())
		}

		return fmt.Sprintf("Upgrade Talos on cluster %q from %s to %s.", cluster, p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_KUBERNETES:
		return fmt.Sprintf("Upgrade Kubernetes on cluster %q from %s to %s.", cluster, p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_ICHOR:
		return fmt.Sprintf("Upgrade Ichor itself from %s to %s.", p.GetCurrent(), to)
	case pb.OperationKind_OPERATION_KIND_CNI:
		if p.GetTarget() == "" || p.GetTarget() == p.GetCurrent() {
			// The spec already says the target, so the first run wrote it to Git and this run
			// starts the operation that switches the cluster; "from X to X" would read as a no-op.
			return fmt.Sprintf("Switch kube-proxy on cluster %q: starting the switch to %s (the spec already says %s).",
				cluster, p.GetCurrent(), p.GetCurrent())
		}

		return fmt.Sprintf("Switch kube-proxy on cluster %q from %s to %s.", cluster, p.GetCurrent(), p.GetTarget())
	case pb.OperationKind_OPERATION_KIND_CA_ROTATION:
		return fmt.Sprintf("Rotate the %s certificate authorities (ca-rotation) of cluster %q.", caPlanNames(p.GetCaTargets()), cluster)
	case pb.OperationKind_OPERATION_KIND_KEY_ROTATION:
		return fmt.Sprintf("Rotate the service-account signing key and the secretbox (encryption at rest) key (key-rotation) of cluster %q.", cluster)
	case pb.OperationKind_OPERATION_KIND_CONFIG:
		if p.GetEffect() == pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS {
			return fmt.Sprintf("Apply the pending configuration changes that restart the Kubernetes API server (config) of cluster %q.", cluster)
		}

		return fmt.Sprintf("Apply the pending reboot-requiring configuration changes (config) of cluster %q.", cluster)
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
	case pb.OperationKind_OPERATION_KIND_KEY_ROTATION:
		b.WriteString(", one at a time, waiting for each kube-apiserver to come back; every one is visited in each applying stage:\n")
	case pb.OperationKind_OPERATION_KIND_KUBERNETES:
		fmt.Fprintf(&b, ": the control planes one at a time in each of the first three stages; in the kubelet stage the control planes "+
			"one at a time, then workers (at most %d at once), in this order:\n", max(p.GetMaxUnavailable(), 1))
	default:
		fmt.Fprintf(&b, ": control planes one at a time, then workers (at most %d at once), in this order:\n", max(p.GetMaxUnavailable(), 1))
	}

	b.WriteString(strings.Join(steps, "\n"))
	b.WriteString("\n")

	for _, m := range p.GetMachines() {
		if f := m.GetRebootFields(); len(f) > 0 {
			fmt.Fprintf(&b, "  %s needs the reboot for: %s\n", m.GetName(), strings.Join(f, ", "))
		}
	}

	if kind == pb.OperationKind_OPERATION_KIND_CONFIG {
		if p.GetEffect() == pb.OperationEffect_OPERATION_EFFECT_REBOOT {
			for _, m := range p.GetMachines() {
				if len(m.GetRebootFields()) == 0 {
					fmt.Fprintf(&b, "  %s only restarts its Kubernetes API server (no reboot)\n", m.GetName())
				}
			}
		}

		b.WriteString("The machine the controller runs on goes last.\n")
	}

	return b.String()
}
