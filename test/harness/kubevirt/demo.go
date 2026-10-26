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

// "up-demo"/"down-demo" stand up a small, long-lived cluster for looking at ichor-ui by
// hand: one schedulable control plane (installed, bootstrapped) and one worker left in
// maintenance mode, so the UI has an approved machine and a pending one to show. Every
// object is named with demoPrefix, never the e2e prefixes, because other runs share the
// ichor-e2e namespace. Unlike the other modes nothing here is torn down on its own:
// the cluster is meant to stay up until someone runs "down-demo".
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	yaml "go.yaml.in/yaml/v4"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const (
	demoPrefix  = "ichor-ui-demo"
	demoCP      = demoPrefix + "-cp"
	demoWorker  = demoPrefix + "-w"
	demoCluster = demoPrefix
)

func demoVMManifest(name string) string {
	m := fmt.Sprintf(haVMManifestTmpl, name, talosFactorySchematic, talosVersion)
	m = strings.ReplaceAll(m, "kubevirt-ha", "kubevirt-ui-demo")
	// The control plane also runs ichor and ichor-ui, so it gets more memory than a bare node.
	return strings.ReplaceAll(m, "guest: 2560Mi", "guest: 4Gi")
}

func upDemo() error {
	names := []string{demoCP, demoWorker}
	for _, n := range names {
		if err := applyManifest(demoVMManifest(n)); err != nil {
			return fmt.Errorf("creating VM %s: %w", n, err)
		}
	}
	ips := map[string]string{}
	for _, n := range names {
		ip, err := pollFor("waiting for "+n+"'s pod IP", 5*time.Minute, func() (string, bool) {
			out, err := utils.Run(kube.Command("-n", namespace, "get", "vmi", n,
				"-o", "jsonpath={.status.interfaces[0].ipAddress}"))
			ip := strings.TrimSpace(out)
			return ip, err == nil && ip != ""
		})
		if err != nil {
			return err
		}
		ips[n] = ip
	}
	for _, n := range names {
		ip := ips[n]
		if _, err := pollFor("waiting for "+n+" ("+ip+") to reach maintenance", 6*time.Minute, func() (string, bool) {
			out, err := talosctl.Get(namespace, ip, "machinestatus")
			if err != nil {
				return "", false
			}
			objs, err := talosctl.DecodeAll(out)
			return "", err == nil && len(objs) == 1 && talosctl.SpecField(objs[0], "stage") == "maintenance"
		}); err != nil {
			return err
		}
	}

	cpIP := ips[demoCP]
	cfg, err := generateControlPlaneConfig(demoCluster, []string{cpIP}, generate.WithAllowSchedulingOnControlPlanes(true))
	if err != nil {
		return err
	}
	tc, err := cfg.talosconfig.Bytes()
	if err != nil {
		return fmt.Errorf("marshaling talosconfig: %w", err)
	}
	if out, err := talosctl.Apply(namespace, cpIP, cfg.machineConfig); err != nil {
		return fmt.Errorf("applying config: %w: %s", err, out)
	}
	if _, err := pollFor("waiting for the authenticated API", 10*time.Minute, func() (string, bool) {
		_, err := talosctl.GetAuthenticated(namespace, cpIP, "machinestatus", tc)
		return "", err == nil
	}); err != nil {
		return err
	}
	if _, err := pollFor("waiting for bootstrap to become available", 2*time.Minute, func() (string, bool) {
		_, err := talosctl.Bootstrap(namespace, cpIP, tc)
		return "", err == nil
	}); err != nil {
		return err
	}
	if _, err := pollFor("waiting for running/ready", 8*time.Minute, func() (string, bool) {
		out, err := talosctl.GetAuthenticated(namespace, cpIP, "machinestatus", tc)
		if err != nil {
			return "", false
		}
		objs, err := talosctl.DecodeAll(out)
		return "", err == nil && len(objs) == 1 && talosctl.SpecField(objs[0], "stage") == "running" && talosctl.MachineStatusReady(objs[0])
	}); err != nil {
		return err
	}

	bin, err := projectBin()
	if err != nil {
		return err
	}
	kc, err := talosctl.Kubeconfig(namespace, cpIP, tc)
	if err != nil {
		return fmt.Errorf("fetching kubeconfig: %w", err)
	}
	secrets, err := yaml.Marshal(cfg.secretsBundle)
	if err != nil {
		return fmt.Errorf("marshaling secrets bundle: %w", err)
	}
	for name, data := range map[string][]byte{
		"ui-demo.talosconfig":  tc,
		"ui-demo.kubeconfig":   kc,
		"ui-demo-secrets.yaml": secrets,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), data, 0o600); err != nil {
			return err
		}
	}
	fmt.Println("CP_IP=" + cpIP)
	fmt.Println("WORKER_IP=" + ips[demoWorker])
	return nil
}

func downDemo() error {
	if _, err := utils.Run(kube.Command("-n", namespace, "delete", "vm", demoCP, demoWorker,
		"--wait=true", "--timeout=120s", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting the VMs: %w", err)
	}
	args := []string{"-n", namespace, "delete", "dv",
		demoCP + "-iso", demoCP + "-system", demoWorker + "-iso", demoWorker + "-system",
		"--wait=true", "--timeout=120s", "--ignore-not-found"}
	if _, err := utils.Run(kube.Command(args...)); err != nil {
		return fmt.Errorf("deleting the DataVolumes: %w", err)
	}
	return nil
}
