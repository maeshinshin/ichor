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

// Command kubevirt is the KubeVirt harness's setup/cleanup for e2e: it boots a
// Talos node as a KubeVirt VM in ichor-e2e and waits for Talos maintenance mode ("up"), or
// tears that VM and its DataVolumes down ("down"). The e2e specs never create or delete
// this VM themselves - they only consume the node endpoint this prints, through
// ICHOR_E2E_TALOS_NODES, exactly as the talosctl-qemu harness's own setup/cleanup do for CI.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const namespace = "ichor-e2e"

// vmName is fixed, not random: "up" and "down" are separate process invocations (separate
// `make`/CI steps) with no shared state between them, so cleanup must be able to find what
// setup created without being told a generated name.
const vmName = "ichor-e2e-short"

// talosFactorySchematic is the bare, no-extensions Image Factory schematic for the
// metal-amd64 ISO.
const talosFactorySchematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// talosVersion is the Talos release the harness's VM boots; it must match talosctl.Image.
const talosVersion = "v1.14.3"

const vmManifestTmpl = `apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %[1]s
  namespace: ichor-e2e
  labels:
    ichor.maesh.dev/e2e-harness: kubevirt
spec:
  runStrategy: Always
  dataVolumeTemplates:
    - metadata:
        name: %[1]s-iso
      spec:
        source:
          http:
            url: https://factory.talos.dev/image/%[2]s/%[3]s/metal-amd64.iso
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: 1Gi
          storageClassName: ceph-block
    - metadata:
        name: %[1]s-system
      spec:
        source:
          blank: {}
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: 10Gi
          storageClassName: ceph-block
  template:
    metadata:
      labels:
        ichor.maesh.dev/e2e-harness: kubevirt
    spec:
      domain:
        cpu:
          cores: 2
        memory:
          guest: 2560Mi
        devices:
          disks:
            - name: iso
              cdrom:
                bus: sata
              bootOrder: 1
            - name: system
              disk:
                bus: virtio
              bootOrder: 2
          interfaces:
            - name: default
              bridge: {}
      networks:
        - name: default
          pod: {}
      volumes:
        - name: iso
          dataVolume:
            name: %[1]s-iso
        - name: system
          dataVolume:
            name: %[1]s-system
`

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: kubevirt up|down")
		os.Exit(2)
	}

	var err error
	if os.Args[1] == "up" {
		err = up()
	} else {
		err = down()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kubevirt harness:", err)
		os.Exit(1)
	}
}

func up() error {
	manifest := fmt.Sprintf(vmManifestTmpl, vmName, talosFactorySchematic, talosVersion)
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("creating the VM: %w", err)
	}

	ip, err := pollFor("waiting for the VMI to report a pod IP", 3*time.Minute, func() (string, bool) {
		out, err := utils.Run(exec.Command("kubectl", "-n", namespace, "get", "vmi", vmName,
			"-o", "jsonpath={.status.interfaces[0].ipAddress}"))
		ip := strings.TrimSpace(out)
		return ip, err == nil && ip != ""
	})
	if err != nil {
		return err
	}

	if _, err := pollFor("waiting for MachineStatus stage to reach maintenance", 5*time.Minute, func() (string, bool) {
		out, err := talosctl.Get(namespace, ip, "machinestatus")
		if err != nil {
			return "", false
		}
		objs, err := talosctl.DecodeAll(out)
		if err != nil || len(objs) != 1 {
			return "", false
		}
		return ip, talosctl.SpecField(objs[0], "stage") == "maintenance"
	}); err != nil {
		return err
	}

	fmt.Println(ip)
	if envFile := os.Getenv("GITHUB_ENV"); envFile != "" {
		f, err := os.OpenFile(envFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("opening GITHUB_ENV: %w", err)
		}
		defer f.Close()
		if _, err := fmt.Fprintf(f, "ICHOR_E2E_TALOS_NODES=%s\n", ip); err != nil {
			return fmt.Errorf("writing GITHUB_ENV: %w", err)
		}
	}
	return nil
}

func down() error {
	if _, err := utils.Run(exec.Command("kubectl", "-n", namespace, "delete", "vm", vmName,
		"--wait=true", "--timeout=120s", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting the VM: %w", err)
	}
	if _, err := utils.Run(exec.Command("kubectl", "-n", namespace, "delete", "dv",
		vmName+"-iso", vmName+"-system", "--wait=true", "--timeout=120s", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting the DataVolumes: %w", err)
	}
	return nil
}

// pollFor retries check until it reports ready, returning its value, or fails after timeout.
func pollFor(step string, timeout time.Duration, check func() (string, bool)) (string, error) {
	fmt.Println(step)
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := check(); ok {
			return v, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%s: timed out after %s", step, timeout)
		}
		time.Sleep(5 * time.Second)
	}
}
