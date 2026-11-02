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

// "up-demo"/"down-demo" create the two VMs of a cluster for looking at ichor-ui by hand
// and leave BOTH in Talos maintenance mode: the harness installs nothing. The cluster
// itself is built by `ichor init` against <prefix>-cp and the worker is approved with
// `ichor machine approve --install-disk`, so every node's configuration is rendered by
// Ichor (test/harness/ui-demo/README.md). Objects are named with a prefix (env
// ICHOR_UI_DEMO_PREFIX, default "ichor-ui-demo"), never the e2e prefixes, because other
// runs share the ichor-e2e namespace. Nothing here is torn down on its own: the cluster
// stays up until someone runs "down-demo" with the same prefix.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
)

const (
	defaultDemoPrefix = "ichor-ui-demo"
	demoPrefixEnv     = "ICHOR_UI_DEMO_PREFIX"

	// defaultDemoMemory is the guest memory baked into the VM template; the two envs
	// below override it per role.
	defaultDemoMemory = "2560Mi"
	demoCPMemoryEnv   = "ICHOR_UI_DEMO_CP_MEMORY"
	demoWorkerMemEnv  = "ICHOR_UI_DEMO_WORKER_MEMORY"
)

var demoMemoryPattern = regexp.MustCompile(`^[1-9][0-9]*(Mi|Gi)$`)

// demoPrefixPattern keeps the prefix a DNS-1123 label fragment: it ends up in VM and
// DataVolume names, so a bad value is refused before anything is created or deleted.
var demoPrefixPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`)

// demoNames returns the control-plane and worker VM names.
func demoNames() (cp, worker string, err error) {
	prefix := os.Getenv(demoPrefixEnv)
	if prefix == "" {
		prefix = defaultDemoPrefix
	}

	if !demoPrefixPattern.MatchString(prefix) {
		return "", "", fmt.Errorf("%s=%q must be a lowercase DNS label of at most 32 characters", demoPrefixEnv, prefix)
	}

	return prefix + "-cp", prefix + "-w", nil
}

// demoHAEnv=1 makes up-demo and down-demo use three control planes (<prefix>-cp, -cp2, -cp3) and
// no worker, for HA verification (ICHOR_UI_DEMO_NO_WORKER is ignored). demoHAEnv=extra creates only
// a fourth control-plane VM, <prefix>-cp4, next to a running HA demo, to replace one of its control planes.
const demoHAEnv = "ICHOR_UI_DEMO_HA"

func demoHA() bool { return os.Getenv(demoHAEnv) == "1" || demoExtra() }

func demoExtra() bool { return os.Getenv(demoHAEnv) == "extra" }

// demoScaleEnv=<control planes>x<workers> (e.g. 5x10) makes up-demo create that many VMs:
// <prefix>-cp, -cp2..-cpN and <prefix>-w, -w2..-wM, and prints every address (scale verification).
// With ICHOR_UI_DEMO_HA=extra the extra control plane is then <prefix>-cp<N+1>. The VMs avoid
// the overcommitted talos02 through a node affinity.
const demoScaleEnv = "ICHOR_UI_DEMO_SCALE"

func demoScale() (cps, workers int) {
	_, _ = fmt.Sscanf(os.Getenv(demoScaleEnv), "%dx%d", &cps, &workers)

	return cps, workers
}

func numbered(base string, n int) []string {
	out := []string{}
	for i := 1; i <= n; i++ {
		if i == 1 {
			out = append(out, base)
		} else {
			out = append(out, fmt.Sprintf("%s%d", base, i))
		}
	}

	return out
}

// demoMemory returns the guest memory for a demo VM: the value of env, or defaultDemoMemory when unset.
// It ends up in the VM manifest, so only a plain Mi/Gi quantity is accepted.
func demoMemory(env string) (string, error) {
	v := os.Getenv(env)
	if v == "" {
		return defaultDemoMemory, nil
	}

	if !demoMemoryPattern.MatchString(v) {
		return "", fmt.Errorf("%s=%q must be a quantity like 2560Mi or 3Gi", env, v)
	}

	return v, nil
}

func demoVMManifest(name, group, memory string, iso isoSource) string {
	m := renderVM(name, "kubevirt-ui-demo", iso)
	// A group label on the launcher pod, so an outer Service can select all of one demo's VMs.
	m = strings.Replace(m, "  template:\n    metadata:\n      labels:\n",
		"  template:\n    metadata:\n      labels:\n        ichor.maesh.dev/e2e-group: "+group+"\n", 1)
	m = strings.Replace(m, "guest: "+defaultDemoMemory, "guest: "+memory, 1)

	if c, _ := demoScale(); c > 0 {
		m = strings.Replace(m, "    spec:\n      domain:\n", "    spec:\n      affinity:\n        nodeAffinity:\n          requiredDuringSchedulingIgnoredDuringExecution:\n            nodeSelectorTerms:\n              - matchExpressions:\n                  - {key: kubernetes.io/hostname, operator: NotIn, values: [talos02]}\n      domain:\n", 1) //nolint:lll
	}

	return m
}

func upDemo() error {
	cp, worker, err := demoNames()
	if err != nil {
		return err
	}

	cpMem, err := demoMemory(demoCPMemoryEnv)
	if err != nil {
		return err
	}

	workerMem, err := demoMemory(demoWorkerMemEnv)
	if err != nil {
		return err
	}

	names := []string{cp, worker}
	memory := map[string]string{cp: cpMem, worker: workerMem}

	// ICHOR_UI_DEMO_NO_WORKER=1 boots the control plane only (single-VM test runs).
	if os.Getenv("ICHOR_UI_DEMO_NO_WORKER") == "1" {
		names = []string{cp}
	}

	if demoHA() {
		names = []string{cp, cp + "2", cp + "3"}
		memory = map[string]string{cp: cpMem, cp + "2": cpMem, cp + "3": cpMem}
	}

	if c, w := demoScale(); c > 0 {
		names = append(numbered(cp, c), numbered(worker, w)...)
		memory = map[string]string{}
		for _, n := range names {
			memory[n] = workerMem
		}
		for _, n := range numbered(cp, c) {
			memory[n] = cpMem
		}
	}

	if demoExtra() {
		extra := cp + "4"
		if c, _ := demoScale(); c > 0 {
			extra = fmt.Sprintf("%s%d", cp, c+1)
		}
		names = []string{extra}
		memory = map[string]string{extra: cpMem}
	}

	iso, err := resolveISO()
	if err != nil {
		return err
	}

	for _, n := range names {
		if err := kube.Apply(demoVMManifest(n, cp[:len(cp)-len("-cp")], memory[n], iso)); err != nil {
			return fmt.Errorf("creating VM %s: %w", n, err)
		}
	}

	ips := map[string]string{}

	for _, n := range names {
		ip, err := waitVMIIP(n, 10*time.Minute)
		if err != nil {
			return err
		}

		ips[n] = ip
	}

	for _, n := range names {
		ip := ips[n]
		if _, err := kube.Poll("waiting for "+n+" ("+ip+") to reach maintenance", 10*time.Minute, func() (string, bool) {
			out, err := talosctl.Get(namespace, ip, "machinestatus")
			if err != nil {
				return "", false
			}

			return "", machineInMaintenance(out)
		}); err != nil {
			return err
		}
	}

	if c, w := demoScale(); c > 0 && !demoExtra() {
		for _, n := range append(numbered(cp, c), numbered(worker, w)...) {
			fmt.Println(strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(n, cp[:len(cp)-len("-cp")]+"-"), "-", "_")) + "_IP=" + ips[n]) //nolint:lll
		}

		return nil
	}

	if demoExtra() {
		for n, ip := range ips {
			fmt.Println("EXTRA_IP=" + ip + " # " + n)
		}

		return nil
	}

	fmt.Println("CP_IP=" + ips[cp])
	if demoHA() {
		fmt.Println("CP2_IP=" + ips[cp+"2"])
		fmt.Println("CP3_IP=" + ips[cp+"3"])

		return nil
	}

	if ips[worker] != "" {
		fmt.Println("WORKER_IP=" + ips[worker])
	}

	return nil
}

func downDemo() error {
	cp, worker, err := demoNames()
	if err != nil {
		return err
	}

	vms := []string{cp, worker}
	if demoHA() {
		vms = []string{cp, cp + "2", cp + "3", cp + "4"}
	}

	if c, w := demoScale(); c > 0 {
		vms = append(numbered(cp, c+1), numbered(worker, w)...)
	}

	return deleteVMs(vms...)
}
