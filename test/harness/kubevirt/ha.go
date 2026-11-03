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

// "up-ha"/"down-ha" extend the harness to three control planes, for the removal-ordering
// scenarios design's "Nodes" section describes (quorum, last-control-plane, transitioning)
// that need real etcd membership and a real kubelet drain -- envtest has neither. A
// separate pair from "up"/"down": the short e2e set's proven single-node path is untouched,
// and this one is heavy enough (~40Gi of Ceph for a few minutes) that it belongs in the
// nightly set only.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

// haCount is the control-plane count this scenario verifies: 3, the smallest topology
// where a single-member removal (3->2) is allowed and etcd quorum math actually does
// something.
const haCount = 3

const verbDelete = "delete"

// machineInMaintenance reports whether a `get machinestatus` output (one document) is at the
// maintenance stage.
func machineInMaintenance(out string) bool {
	objs, err := talosctl.DecodeAll(out)
	return err == nil && len(objs) == 1 && talosctl.SpecField(objs[0], "stage") == "maintenance"
}

// machineRunning reports whether a `get machinestatus` output is at the running stage and,
// when needReady, the node also reports ready.
func machineRunning(out string, needReady bool) bool {
	objs, err := talosctl.DecodeAll(out)
	if err != nil || len(objs) != 1 || talosctl.SpecField(objs[0], "stage") != "running" {
		return false
	}
	return !needReady || talosctl.MachineStatusReady(objs[0])
}

// haCluster names every object this scenario creates; haVMName derives each VM's own name
// from it, fixed like vmName (see its own comment) for the same cross-invocation reason.
const haCluster = "ichor-e2e-ha"

// haMaxVMs bounds the names "down-ha" deletes (--ignore-not-found), so a run started with
// workers is cleaned up without being told how many it had.
const haMaxVMs = 8

// haTotal is the control planes plus ICHOR_E2E_HA_WORKERS workers.
func haTotal() int {
	n, _ := strconv.Atoi(os.Getenv(workersEnv))
	return haCount + min(max(n, 0), haMaxVMs-haCount)
}

func haVMName(i int) string { return fmt.Sprintf("%s-%d", haCluster, i) }

// Each VM gets its own ISO and system DataVolume (vmManifestTmpl in main.go): one ISO DataVolume
// shared read-only across VMs does not work on this cluster's storage, because rook-ceph's RBD CSI
// driver rejects ReadOnlyMany for a plain CDI HTTP import.

func upHA() error {
	ips, err := bootHAToMaintenance()
	if err != nil {
		return err
	}
	talosconfigYAML, bundle, err := configureAndBootstrapHA(ips)
	if err != nil {
		return err
	}
	return publishHA(ips, talosconfigYAML, bundle)
}

// bootHAToMaintenance creates the shared ISO DataVolume and haCount VMs referencing it,
// and waits for every VM to report a pod IP and reach maintenance.
func bootHAToMaintenance() ([]string, error) {
	iso, err := resolveISO()
	if err != nil {
		return nil, err
	}

	for i := range haTotal() {
		name := haVMName(i)
		if err := kube.Apply(renderVM(name, "kubevirt-ha", iso)); err != nil {
			return nil, fmt.Errorf("creating VM %s: %w", name, err)
		}
	}

	ips := make([]string, haTotal())
	for i := range haTotal() {
		name := haVMName(i)
		ip, err := waitVMIIP(name, 12*time.Minute)
		if err != nil {
			return nil, err
		}
		ips[i] = ip
	}

	for i, ip := range ips {
		step := "waiting for " + haVMName(i) + " (" + ip + ") to reach maintenance"
		if err := waitForMaintenance(step, ip, 5*time.Minute); err != nil {
			return nil, err
		}
	}
	fmt.Println("--- all three VMs in maintenance mode, pod IPs:", ips, "---")
	return ips, nil
}

// configureAndBootstrapHA applies one control-plane config to every node in ips, waits
// for each one's authenticated API, bootstraps once on ips[0], and waits for every node
// to report running/ready. It returns the admin talosconfig that authenticates against
// the whole cluster, and the secrets bundle it (and every node's machine config) was
// generated from -- the removal-ordering verification needs the real bundle to
// dial these same nodes' real Talos API, not a fresh unrelated one.
func configureAndBootstrapHA(ips []string) ([]byte, *secrets.Bundle, error) {
	cfg, err := generateControlPlaneConfig(haCluster, ips[:haCount])
	if err != nil {
		return nil, nil, err
	}
	talosconfigYAML, err := cfg.talosconfig.Bytes()
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling talosconfig: %w", err)
	}

	// The same rendered control-plane config applies to every node (config.go's own
	// comment): no per-node fields differ.
	for i, ip := range ips {
		fmt.Printf("--- applying the control-plane config to %s (installs and reboots) ---\n", haVMName(i))
		config := cfg.machineConfig
		if i >= haCount {
			config = cfg.workerConfig
		}
		out, err := talosctl.Apply(namespace, ip, config)
		if err != nil {
			return nil, nil, fmt.Errorf("applying config to %s: %w: %s", ip, err, out)
		}
	}

	for i, ip := range ips {
		step := "waiting for " + haVMName(i) + "'s authenticated API"
		if _, err := kube.Poll(step, 10*time.Minute, func() (string, bool) {
			_, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", talosconfigYAML)
			return "", err == nil
		}); err != nil {
			return nil, nil, err
		}
	}

	// Bootstrap runs once, on the first control plane: Talos's own controllers on the
	// other two discover it through the endpoint list already baked into their config and
	// join etcd on their own (architecture.md "Nodes" never calls bootstrap per-node either).
	bootstrapOut, err := kube.Poll("waiting for bootstrap to become available", 2*time.Minute, func() (string, bool) {
		out, err := talosctl.Bootstrap(namespace, ips[0], talosconfigYAML)
		return out, err == nil
	})
	if err != nil {
		return nil, nil, err
	}
	fmt.Println("--- bootstrap (on", haVMName(0), ") ---")
	fmt.Println(bootstrapOut)

	for i, ip := range ips {
		step := "waiting for " + haVMName(i) + " to report running/ready"
		if _, err := kube.Poll(step, 8*time.Minute, func() (string, bool) {
			out, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", talosconfigYAML)
			if err != nil {
				return "", false
			}
			// With no CNI the node cannot report ready (its Kubernetes node stays NotReady), so only
			// the stage is awaited; the test installs the CNI afterwards.
			return "", machineRunning(out, false)
		}); err != nil {
			return nil, nil, err
		}
	}
	return talosconfigYAML, cfg.secretsBundle, nil
}

// publishHA prints the cluster-health evidence (talosctl health, etcd members) and writes
// the talosconfig/kubeconfig files the removal-ordering verification reads afterwards.
func publishHA(ips []string, talosconfigYAML []byte, bundle *secrets.Bundle) error {
	// The nodes boot with no CNI (the generator's flannel is always dropped), so
	// `talosctl health` would fail on "all k8s nodes to report ready"; Cilium is installed afterwards.
	membersOut, err := talosctl.EtcdMembers(namespace, ips[0], talosconfigYAML)
	if err != nil {
		return fmt.Errorf("etcd members: %w", err)
	}
	fmt.Println("--- etcd members ---")
	fmt.Println(membersOut)

	bin, err := projectBin()
	if err != nil {
		return err
	}
	talosconfigPath := filepath.Join(bin, "test-e2e-kubevirt-ha.talosconfig")
	if err := os.WriteFile(talosconfigPath, talosconfigYAML, 0o600); err != nil {
		return fmt.Errorf("writing talosconfig: %w", err)
	}
	kubeconfigYAML, err := talosctl.Kubeconfig(namespace, ips[0], talosconfigYAML)
	if err != nil {
		return fmt.Errorf("fetching kubeconfig: %w", err)
	}
	kubeconfigPath := filepath.Join(bin, "test-e2e-kubevirt-ha.kubeconfig")
	if err := os.WriteFile(kubeconfigPath, kubeconfigYAML, 0o600); err != nil {
		return fmt.Errorf("writing kubeconfig: %w", err)
	}

	// The real secrets bundle these nodes were configured from, in the same shape
	// internal/controller/bundle.go's loadBundle reads out of a Secret: a TalosCluster fixture referencing it can dial
	// these nodes' real
	// Talos API with credentials that actually authenticate.
	secretsYAML, err := yaml.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshaling secrets bundle: %w", err)
	}
	secretsPath := filepath.Join(bin, "test-e2e-kubevirt-ha-secrets.yaml")
	if err := os.WriteFile(secretsPath, secretsYAML, 0o600); err != nil {
		return fmt.Errorf("writing secrets bundle: %w", err)
	}

	fmt.Println(strings.Join(ips, ","))
	fmt.Println("ICHOR_E2E_HA_TALOSCONFIG=" + talosconfigPath)
	fmt.Println("ICHOR_E2E_HA_SECRETS=" + secretsPath)
	fmt.Println("ICHOR_E2E_HA_KUBECONFIG=" + kubeconfigPath)
	return nil
}

// downHA tears down every object upHA creates, in the reverse order, exactly like down().
func downHA() error {
	names := make([]string, haMaxVMs)
	for i := range haMaxVMs {
		names[i] = haVMName(i)
	}
	return deleteVMs(names...)
}

// deleteVMs deletes the named VMs, then their "-iso" and "-system" DataVolumes; absent ones are fine.
func deleteVMs(names ...string) error {
	dvs := make([]string, 0, len(names)*2)
	for _, n := range names {
		dvs = append(dvs, n+"-iso", n+"-system")
	}
	for _, kind := range []struct {
		resource, label string
		names           []string
	}{{"vm", "VMs", names}, {"dv", "DataVolumes", dvs}} {
		args := append([]string{"-n", namespace, verbDelete, kind.resource}, kind.names...)
		args = append(args, "--wait=true", "--timeout=120s", "--ignore-not-found")
		if _, err := utils.Run(kube.Command(args...)); err != nil {
			return fmt.Errorf("deleting the %s: %w", kind.label, err)
		}
	}
	return nil
}
