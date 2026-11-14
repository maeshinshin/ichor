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
// Talos node as a KubeVirt VM in ichor-e2e, waits for maintenance mode, applies a
// control-plane configuration, waits for the authenticated API, bootstraps etcd, and hands
// back an inner-cluster kubeconfig and the node's address ("up") - or tears everything down
// ("down"). The e2e specs never provision any of this themselves; they only consume what
// "up" publishes, exactly as the talosctl-qemu harness's own setup/cleanup do for CI.
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const namespace = "ichor-e2e"

// vmName is fixed, not random: "up" and "down" are separate process invocations (separate
// `make`/CI steps) with no shared state between them, so cleanup must be able to find what
// setup created without being told a generated name.
const vmName = "ichor-e2e-short"

// relayName is a plain (non-bridge-bound) Pod that proxies TCP to the VM's own address: the
// workstation running this harness cannot reach the VM's pod IP directly, and
// `kubectl port-forward` refuses bridge-bound pods, but it works fine against an ordinary
// one like this (Boundaries).
const relayName = vmName + "-relay"

// relayLocalPort is the fixed local port the harness port-forwards to the relay on.
//
// shortcut: a fixed port risks colliding with something else already using it on the host
// running the harness; upgrade to an ephemeral port (parsed from `kubectl port-forward`'s own
// "Forwarding from 127.0.0.1:NNNNN" output) the first time that happens.
const relayLocalPort = "16443"

// talosFactorySchematic is the bare, no-extensions Image Factory schematic for the
// metal-amd64 ISO.
const talosFactorySchematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// talosVersion is the Talos release the harness's VM boots; it must match talosctl.Image.
const talosVersion = "v1.14.5"

const vmManifestTmpl = `apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %[1]s
  namespace: ichor-e2e
  labels:
    ichor.maesh.dev/e2e-harness: %[4]s
spec:
  runStrategy: Always
  dataVolumeTemplates:
    - metadata:
        name: %[1]s-iso
      spec:
        source:
%[2]s
        storage:
          accessModes: [ReadWriteOnce]
          volumeMode: Filesystem
          resources:
            requests:
              storage: %[3]s
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
        ichor.maesh.dev/e2e-harness: %[4]s
    spec:
      domain:
        cpu:
          cores: 2
        memory:
          guest: 2560Mi
        devices:
          disks:
            # The system disk boots first: a blank disk is not bootable, so the first boot falls through
            # to the ISO (maintenance mode), and after the install every cold boot (a VM stopped and
            # started) comes up from the disk. With the ISO first, Talos halts on a cold boot
            # ("already installed to disk but booted from another media").
            - name: iso
              cdrom:
                bus: sata
              bootOrder: 2
            - name: system
              disk:
                bus: virtio
              bootOrder: 1
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

// relayManifestTmpl is a plain pod (no KubeVirt involved) that relays TCP :6443 to the VM's
// own address, so the harness can reach it from the workstation through a normal
// `kubectl port-forward` (see relayName).
const relayManifestTmpl = `apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: ichor-e2e
  labels:
    ichor.maesh.dev/e2e-harness: kubevirt
spec:
  restartPolicy: Never
  containers:
    - name: relay
      image: alpine/socat
      args: ["TCP-LISTEN:6443,fork,reuseaddr", "TCP:%[2]s:6443"]
`

// upFns and downFns pair each mode with its own up/down, so main's dispatch, the
// failed-up cleanup and the signal handler all share one place that knows which "down"
// undoes which "up" - "up-ha"/"down-ha" (ha.go) is the three-control-plane scenario; "up"/"down" is the short e2e
// set's proven single-node one.
var (
	upFns = map[string]func() error{
		"up":            up,
		"up-ha":         upHA,
		"up-controller": upController,
		"up-demo":       upDemo,
	}
	downFns = map[string]func() error{
		"up":              down,
		"down":            down,
		"up-ha":           downHA,
		"down-ha":         downHA,
		"up-controller":   downController,
		"down-controller": downController,
		"up-demo":         downDemo,
		"down-demo":       downDemo,
	}
)

func main() {
	if len(os.Args) != 2 || downFns[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: kubevirt up|down|up-ha|down-ha|up-controller|down-controller|up-demo|down-demo")
		os.Exit(2)
	}
	mode := os.Args[1]

	// Every outer-cluster kubectl call goes through test/internal/kube, which pins it to
	// --context $ICHOR_E2E_KUBE_CONTEXT and confines deletes to ichor-e2e. Refuse up front
	// (before any "up" creates something) rather than fall back to kubectl's ambient
	// current-context, on which "down" could delete from a cluster this run never touched
	// (observed for real: a dangling kind-kind context).
	if err := kube.VerifyContextExists(); err != nil {
		fmt.Fprintln(os.Stderr, "kubevirt harness:", err)
		os.Exit(1)
	}

	up, isUp := upFns[mode]
	down := downFns[mode]

	var err error
	if isUp {
		// A killed "up"/"up-ha" (Ctrl-C, CI cancellation, a wrapping `timeout`) must not
		// leave the VM(s)/DataVolume(s)/relay behind either - that is the other way a run
		// can end partway through.
		cleanUpOnSignal(down)
		if err = up(); err != nil {
			// A half-finished "up" must not leave the VM, its DataVolumes or the relay
			// pod behind: that keeps Ceph storage pinned for nothing. Clean up before reporting the failure.
			fmt.Fprintln(os.Stderr, "kubevirt harness:", mode, "failed, cleaning up:", err)
			if cleanupErr := down(); cleanupErr != nil {
				fmt.Fprintln(os.Stderr, "kubevirt harness: cleanup after failed", mode, "also failed:", cleanupErr)
			}
		}
	} else {
		err = down()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kubevirt harness:", err)
		os.Exit(1)
	}
}

// cleanUpOnSignal runs down() if the process receives SIGINT or SIGTERM, so a killed "up"
// never leaves the VM, its DataVolumes or the relay behind.
func cleanUpOnSignal(down func() error) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-ch
		fmt.Fprintln(os.Stderr, "kubevirt harness: received", sig, "- cleaning up before exit")
		if err := down(); err != nil {
			fmt.Fprintln(os.Stderr, "kubevirt harness: cleanup after signal failed:", err)
		}
		os.Exit(1)
	}()
}

func up() error {
	ip, err := bootToMaintenance()
	if err != nil {
		return err
	}

	talosconfigYAML, err := installAndBootstrap(ip)
	if err != nil {
		return err
	}

	return publish(ip, talosconfigYAML)
}

// bootToMaintenance creates the VM, waits for its pod IP and for MachineStatus to reach
// maintenance, and prints the maintenance-mode resources.
func bootToMaintenance() (string, error) {
	iso, err := resolveISO()
	if err != nil {
		return "", err
	}
	if err := kube.Apply(renderVM(vmName, "kubevirt", iso)); err != nil {
		return "", fmt.Errorf("creating the VM: %w", err)
	}

	ip, err := waitVMIIP(vmName, 3*time.Minute)
	if err != nil {
		return "", err
	}

	if err := waitForMaintenance("waiting for MachineStatus stage to reach maintenance", ip, 5*time.Minute); err != nil {
		return "", err
	}

	fmt.Println("--- maintenance mode: machinestatus, disks, systeminformation ---")
	for _, resource := range []string{"machinestatus", "disks", "systeminformation"} {
		out, err := talosctl.Get(namespace, ip, resource)
		if err != nil {
			return "", fmt.Errorf("get %s in maintenance mode: %w", resource, err)
		}
		fmt.Println(out)
	}
	return ip, nil
}

// waitVMIIP waits for the VMI called name to report its pod IP.
func waitVMIIP(name string, timeout time.Duration) (string, error) {
	return kube.Poll("waiting for "+name+"'s VMI to report a pod IP", timeout, func() (string, bool) {
		ip, err := getJSONPath("vmi", name, ".status.interfaces[0].ipAddress")
		return ip, err == nil && ip != ""
	})
}

// getJSONPath returns the trimmed value of expr (a JSONPath without its braces) on the named
// resource in ichor-e2e on the outer cluster.
func getJSONPath(resource, name, expr string) (string, error) {
	out, err := utils.Run(kube.Command("-n", namespace, "get", resource, name, "-o", "jsonpath={"+expr+"}"))
	return strings.TrimSpace(out), err
}

// waitForMaintenance polls ip's machinestatus until the node reports the maintenance stage.
func waitForMaintenance(step, ip string, timeout time.Duration) error {
	_, err := kube.Poll(step, timeout, func() (string, bool) {
		out, err := talosctl.Get(namespace, ip, "machinestatus")
		if err != nil {
			return "", false
		}
		return "", machineInMaintenance(out)
	})
	return err
}

// installAndBootstrap applies a control-plane config to the node at ip (which installs and
// reboots it), waits for the authenticated API, bootstraps etcd, and waits for the node to
// report running/ready. It returns the admin talosconfig that authenticates against it.
func installAndBootstrap(ip string) ([]byte, error) {
	cfg, err := generateControlPlaneConfig(vmName, []string{ip})
	if err != nil {
		return nil, err
	}
	talosconfigYAML, err := cfg.talosconfig.Bytes()
	if err != nil {
		return nil, fmt.Errorf("marshaling talosconfig: %w", err)
	}

	fmt.Println("--- applying the control-plane config (installs and reboots) ---")
	out, err := talosctl.Apply(namespace, ip, cfg.machineConfig)
	if err != nil {
		return nil, fmt.Errorf("applying config: %w: %s", err, out)
	}
	fmt.Println(out)

	if _, err := kube.Poll("waiting for the authenticated API to come up", 10*time.Minute, func() (string, bool) {
		_, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", talosconfigYAML)
		return "", err == nil
	}); err != nil {
		return nil, err
	}

	versionOut, err := talosctl.Version(namespace, ip, talosconfigYAML)
	if err != nil {
		return nil, fmt.Errorf("authenticated version check: %w: %s", err, versionOut)
	}
	fmt.Println("--- authenticated talosctl version ---")
	fmt.Println(versionOut)

	// The authenticated API answering machinestatus doesn't mean etcd's bootstrap RPC is
	// registered yet: it can return "FailedPrecondition: bootstrap
	// is not available yet" right after the version check above succeeded. Retry rather
	// than treat it as fatal.
	var bootstrapOut string
	var bootstrapErr error
	if _, err := kube.Poll("waiting for bootstrap to become available", 2*time.Minute, func() (string, bool) {
		bootstrapOut, bootstrapErr = talosctl.Bootstrap(namespace, ip, talosconfigYAML)
		return "", bootstrapErr == nil
	}); err != nil {
		return nil, fmt.Errorf("bootstrap: %w: %s (last attempt: %v)", err, bootstrapOut, bootstrapErr)
	}
	fmt.Println("--- bootstrap ---")
	fmt.Println(bootstrapOut)

	var lastStatus string
	if _, err := kube.Poll("waiting for machinestatus stage=running ready=true", 5*time.Minute, func() (string, bool) {
		out, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", talosconfigYAML)
		if err != nil {
			lastStatus = err.Error()
			return "", false
		}
		lastStatus = out
		return "", machineRunning(out, true)
	}); err != nil {
		return nil, fmt.Errorf("%w (last machinestatus: %s)", err, lastStatus)
	}
	fmt.Println("--- machinestatus: running, ready ---")
	finalStatus, err := talosctl.GetAuthenticated(namespace, ip, "machinestatus", talosconfigYAML)
	if err != nil {
		return nil, err
	}
	fmt.Println(finalStatus)
	return talosconfigYAML, nil
}

// publish fetches the inner cluster's kubeconfig, starts the relay tunnel, rewrites the
// kubeconfig to go through it, writes both config files, waits for the node to report
// Ready through the tunnel, and publishes everything the e2e suite needs.
func publish(ip string, talosconfigYAML []byte) error {
	kubeconfigYAML, err := talosctl.Kubeconfig(namespace, ip, talosconfigYAML)
	if err != nil {
		return fmt.Errorf("fetching kubeconfig: %w", err)
	}

	if err := startRelay(ip); err != nil {
		return err
	}

	// The node's own endpoint is what we pinned the cluster to; the inner
	// kubeconfig's server field is set to the same value, so rewriting it to the local
	// tunnel is a plain substring replace.
	nodeEndpoint := fmt.Sprintf("https://%s:6443", ip)
	localEndpoint := fmt.Sprintf("https://127.0.0.1:%s", relayLocalPort)
	kubeconfigYAML = []byte(strings.ReplaceAll(string(kubeconfigYAML), nodeEndpoint, localEndpoint))

	bin, err := projectBin()
	if err != nil {
		return err
	}
	innerKubeconfigPath := filepath.Join(bin, "test-e2e-kubevirt.kubeconfig")
	talosconfigPath := filepath.Join(bin, "test-e2e-kubevirt.talosconfig")
	if err := os.WriteFile(innerKubeconfigPath, kubeconfigYAML, 0o600); err != nil {
		return fmt.Errorf("writing inner kubeconfig: %w", err)
	}
	if err := os.WriteFile(talosconfigPath, talosconfigYAML, 0o600); err != nil {
		return fmt.Errorf("writing talosconfig: %w", err)
	}

	if _, err := kube.Poll("waiting for one Ready node through the tunnel", 3*time.Minute, func() (string, bool) {
		out, err := utils.Run(kube.InnerCommand(innerKubeconfigPath, "get", "nodes",
			"-o", "jsonpath={.items[*].status.conditions[?(@.type==\"Ready\")].status}"))
		return out, err == nil && strings.TrimSpace(out) == "True"
	}); err != nil {
		return err
	}
	nodesOut, err := utils.Run(kube.InnerCommand(innerKubeconfigPath, "get", "nodes"))
	if err != nil {
		return err
	}
	fmt.Println("--- kubectl get nodes (inner cluster) ---")
	fmt.Println(nodesOut)

	fmt.Println(ip)
	fmt.Println("ICHOR_E2E_INNER_KUBECONFIG=" + innerKubeconfigPath)
	fmt.Println("ICHOR_E2E_TALOSCONFIG=" + talosconfigPath)
	return writeGithubEnv(ip, innerKubeconfigPath, talosconfigPath)
}

// writeGithubEnv appends this run's published variables to GITHUB_ENV, if set; a no-op
// outside CI.
func writeGithubEnv(ip, innerKubeconfigPath, talosconfigPath string) error {
	envFile := os.Getenv("GITHUB_ENV")
	if envFile == "" {
		return nil
	}
	f, err := os.OpenFile(envFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening GITHUB_ENV: %w", err)
	}
	// We wrote to f, so a failed Close (e.g. a flush error) can mean the write never
	// reached disk - that must surface, not be discarded.
	if _, err := fmt.Fprintf(f, "ICHOR_E2E_TALOS_NODES=%s\nICHOR_E2E_INNER_KUBECONFIG=%s\nICHOR_E2E_TALOSCONFIG=%s\n",
		ip, innerKubeconfigPath, talosconfigPath); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing GITHUB_ENV: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing GITHUB_ENV: %w", err)
	}
	return nil
}

func down() error {
	if pid, err := os.ReadFile(portForwardPIDPath()); err == nil {
		killPortForward(strings.TrimSpace(string(pid)))
		_ = os.Remove(portForwardPIDPath())
	}
	if _, err := utils.Run(kube.Command("-n", namespace, verbDelete, "pod", relayName,
		"--wait=false", "--ignore-not-found")); err != nil {
		return fmt.Errorf("deleting the relay pod: %w", err)
	}
	return deleteVMs(vmName)
}

// startRelay creates the relay pod pointed at nodeIP, waits for it to run, and starts a
// detached `kubectl port-forward` to it that outlives this process - it has to stay up for
// the lifetime of the whole e2e run, which is a separate `make` invocation.
func startRelay(nodeIP string) error {
	manifest := fmt.Sprintf(relayManifestTmpl, relayName, nodeIP)
	if err := kube.Apply(manifest); err != nil {
		return fmt.Errorf("creating the relay pod: %w", err)
	}
	if _, err := utils.Run(kube.Command("-n", namespace, "wait", "pod/"+relayName,
		"--for=condition=Ready", "--timeout=60s")); err != nil {
		return fmt.Errorf("waiting for the relay pod: %w", err)
	}

	dir, err := utils.GetProjectDir()
	if err != nil {
		return err
	}
	pf := kube.Command("-n", namespace, "port-forward", "pod/"+relayName,
		relayLocalPort+":6443", "--address=127.0.0.1")
	pf.Dir = dir
	logFile, err := os.Create(filepath.Join(dir, "bin", "test-e2e-kubevirt.portforward.log"))
	if err != nil {
		return fmt.Errorf("creating port-forward log: %w", err)
	}
	pf.Stdout = logFile
	pf.Stderr = logFile
	pf.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := pf.Start(); err != nil {
		return fmt.Errorf("starting port-forward: %w", err)
	}
	if err := os.WriteFile(portForwardPIDPath(), []byte(fmt.Sprintf("%d", pf.Process.Pid)), 0o600); err != nil {
		return fmt.Errorf("recording port-forward pid: %w", err)
	}

	_, err = kube.Poll("waiting for the port-forward tunnel to accept connections", 30*time.Second, func() (string, bool) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+relayLocalPort, 2*time.Second)
		if err != nil {
			return "", false
		}
		_ = conn.Close()
		return "", true
	})
	return err
}

func killPortForward(pid string) {
	p, err := strconv.Atoi(pid)
	if err != nil || p <= 0 {
		return
	}
	_ = syscall.Kill(p, syscall.SIGTERM)
}

func portForwardPIDPath() string {
	dir, err := utils.GetProjectDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bin", "test-e2e-kubevirt.portforward.pid")
}

func projectBin() (string, error) {
	dir, err := utils.GetProjectDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", bin, err)
	}
	return bin, nil
}
