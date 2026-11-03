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

// Package talosctl runs talosctl against a Talos node from a one-shot Pod inside the
// cluster.
//
// Neither the workstation running `go test`/`go run` nor plain `kubectl port-forward` to
// the node's pod can reach it: with KubeVirt's `bridge` binding the pod IP belongs to the
// guest, so the pod's own network namespace has no listener for port-forward to dial, and
// the attempt is refused (confirmed by hand against a real VM: port-forward gets a flat
// connection refused, while a pod-to-pod connection to the same address succeeds). A Pod
// running ghcr.io/siderolabs/talosctl, created/awaited/read/deleted through kubectl,
// sidesteps that - and is also how the controller will reach nodes in production.
package talosctl

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/utils"
)

// Image is the talosctl release this package runs; it must match the Talos version the
// e2e nodes boot (ADR 0007 pins the release set's Talos, v1.14.5).
var Image = cmp.Or(os.Getenv("ICHOR_E2E_TALOSCTL_IMAGE"), "ghcr.io/siderolabs/talosctl:v1.14.5") // override for an older Talos (ADR 0103 B8)

// nodesFlag, talosconfigFlag and talosconfigKey name the repeated CLI flag and secret-data
// key every authenticated call below shares (goconst).
const (
	nodesFlag        = "--nodes"
	talosconfigFlag  = "--talosconfig"
	talosconfigMount = "/secrets/talosconfig"
	talosconfigKey   = "talosconfig"
)

// Get runs `talosctl get <resource>` against node's insecure maintenance API from a one-shot
// Pod in namespace, and returns its raw (possibly multi-object) JSON output.
func Get(namespace, node, resource string) (string, error) {
	return run(namespace, []string{"get", resource, "-o", "json", "--insecure", nodesFlag, node}, nil, time.Minute)
}

// GetAuthenticated is Get against a configured node's authenticated API, using talosconfig
// (an admin talosconfig's YAML, as produced by the config generator) instead of the insecure
// maintenance service.
func GetAuthenticated(namespace, node, resource string, talosconfig []byte) (string, error) {
	return run(namespace,
		[]string{"get", resource, "-o", "json", talosconfigFlag, talosconfigMount, nodesFlag, node},
		map[string]string{talosconfigKey: string(talosconfig)}, time.Minute)
}

// Apply applies machineConfig to node's insecure maintenance API, which installs Talos and
// reboots into it (ADR 0007). It can take a while: the installer image has to be pulled
// before the node can write it to disk.
func Apply(namespace, node string, machineConfig []byte) (string, error) {
	return run(namespace,
		[]string{"apply-config", "--insecure", nodesFlag, node, "-f", "/secrets/config.yaml"},
		map[string]string{"config.yaml": string(machineConfig)}, 5*time.Minute)
}

// Bootstrap bootstraps etcd on node's authenticated API.
func Bootstrap(namespace, node string, talosconfig []byte) (string, error) {
	return run(namespace,
		[]string{"bootstrap", talosconfigFlag, talosconfigMount, nodesFlag, node},
		map[string]string{talosconfigKey: string(talosconfig)}, time.Minute)
}

// Version runs the authenticated `talosctl version` (no --insecure) against node.
func Version(namespace, node string, talosconfig []byte) (string, error) {
	return run(namespace,
		[]string{"version", talosconfigFlag, talosconfigMount, nodesFlag, node},
		map[string]string{talosconfigKey: string(talosconfig)}, time.Minute)
}

// Upgrade runs `talosctl upgrade --image <image> --wait` against node's authenticated API.
func Upgrade(namespace, node, image string, talosconfig []byte) (string, error) {
	return run(namespace,
		[]string{"upgrade", "--image", image, "--wait", "--timeout", "9m", talosconfigFlag, talosconfigMount, nodesFlag, node},
		map[string]string{talosconfigKey: string(talosconfig)}, 10*time.Minute)
}

// Kubeconfig downloads the admin kubeconfig from node's authenticated API, unmerged.
func Kubeconfig(namespace, node string, talosconfig []byte) ([]byte, error) {
	out, err := run(namespace,
		[]string{"kubeconfig", "-", "--merge=false", talosconfigFlag, talosconfigMount, nodesFlag, node, "--endpoints", node},
		map[string]string{talosconfigKey: string(talosconfig)}, time.Minute)
	return []byte(out), err
}

// Health runs the authenticated `talosctl health`, waiting for etcd, Kubernetes
// components and every control-plane node to report healthy across the whole cluster --
// not just one node's own view.
func Health(namespace string, controlPlaneNodes []string, talosconfig []byte) (string, error) {
	// --nodes (-n) targets exactly one node to run the check from; --control-plane-nodes
	// is the list health checks against. Confirmed for real: "command health requires
	// exactly one node" when --nodes carried the full list too.
	return run(namespace,
		[]string{"health", "--control-plane-nodes", strings.Join(controlPlaneNodes, ","),
			talosconfigFlag, talosconfigMount, nodesFlag, controlPlaneNodes[0]},
		map[string]string{talosconfigKey: string(talosconfig)}, 3*time.Minute)
}

// EtcdMembers runs the authenticated `talosctl etcd members` against node, listing etcd's
// own view of cluster membership -- direct evidence of a membership change, not a count
// derived from TalosMachine objects.
func EtcdMembers(namespace, node string, talosconfig []byte) (string, error) {
	return run(namespace,
		[]string{"etcd", "members", talosconfigFlag, talosconfigMount, nodesFlag, node},
		map[string]string{talosconfigKey: string(talosconfig)}, time.Minute)
}

// DecodeAll decodes the concatenated JSON objects talosctl prints for multi-instance
// resources (it is not a JSON array) into a slice of maps.
func DecodeAll(raw string) ([]map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	var out []map[string]any
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
		out = append(out, obj)
	}
}

// SpecField reads a field out of a decoded resource's "spec".
func SpecField(obj map[string]any, field string) any {
	spec, _ := obj["spec"].(map[string]any)
	return spec[field]
}

// MachineStatusReady reports a decoded MachineStatus resource's readiness. It is nested at
// spec.status.ready, not spec.ready - confirmed against a real node's output (ADR 0007):
// spec.stage is a plain string field, but spec.status is itself an object carrying ready and
// unmetConditions.
func MachineStatusReady(obj map[string]any) bool {
	status, _ := SpecField(obj, "status").(map[string]any)
	ready, _ := status["ready"].(bool)
	return ready
}

// MetadataID reads a decoded resource's "metadata.id".
func MetadataID(obj map[string]any) any {
	metadata, _ := obj["metadata"].(map[string]any)
	return metadata["id"]
}

// run creates a one-shot Pod in namespace running talosctl with args, optionally mounting
// secretData (written to a throwaway Secret, one file per key under /secrets) read-only,
// waits up to timeout for the pod to reach a terminal phase, and returns its combined
// stdout/stderr. The Pod and any Secret are deleted before returning.
func run(namespace string, args []string, secretData map[string]string, timeout time.Duration) (string, error) {
	if err := checkTargets(args); err != nil {
		return "", err
	}

	id := fmt.Sprintf("talosctl-%d", time.Now().UnixNano())

	var secretName string
	if len(secretData) > 0 {
		secretName = id + "-secrets"
		if err := applySecret(namespace, secretName, secretData); err != nil {
			return "", fmt.Errorf("creating secret: %w", err)
		}
		defer deleteSecret(namespace, secretName)
	}

	cmd := kube.Command("apply", "-f", "-")
	cmd.Stdin = strings.NewReader(podManifest(id, namespace, args, secretName))
	if _, err := utils.Run(cmd); err != nil {
		return "", err
	}
	defer func() {
		_, _ = utils.Run(kube.Command("-n", namespace, "delete", "pod", id, "--wait=false", "--ignore-not-found"))
	}()

	phase, waitErr := waitTerminal(namespace, id, timeout)
	logs, logErr := utils.Run(kube.Command("-n", namespace, "logs", id))
	if waitErr != nil {
		return logs, waitErr
	}
	if logErr != nil {
		return "", logErr
	}
	if phase != "Succeeded" {
		return logs, fmt.Errorf("talosctl %v failed (pod phase %s): %s", args, phase, logs)
	}
	return logs, nil
}

// protectedNodes are the production home-lab control planes; no e2e path may address them.
var protectedNodes = map[string]bool{"192.168.0.201": true, "192.168.0.202": true, "192.168.0.203": true}

// checkTargets requires an explicit, non-protected --nodes on every call (so talosctl never
// falls back to a talosconfig's default nodes) and refuses protected addresses anywhere in
// args. Every call here runs in a pod with only the talosconfig this package mounts, but
// the node list is the thing that decides what gets touched, so it is checked on its own.
func checkTargets(args []string) error {
	hasNodes := false
	for i, a := range args {
		for f := range strings.SplitSeq(a, ",") {
			if protectedNodes[strings.TrimSpace(f)] {
				return fmt.Errorf("refusing talosctl %v: %s is a protected production node", args, f)
			}
		}
		if a == nodesFlag && i+1 < len(args) && strings.TrimSpace(args[i+1]) != "" {
			hasNodes = true
		}
	}
	if !hasNodes {
		return fmt.Errorf("refusing talosctl %v: no explicit %s", args, nodesFlag)
	}
	return nil
}

// waitTerminal polls the pod's phase until it reaches Succeeded or Failed, or times out.
func waitTerminal(namespace, podName string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		phase, _ := utils.Run(kube.Command("-n", namespace, "get", "pod", podName,
			"-o", "jsonpath={.status.phase}"))
		phase = strings.TrimSpace(phase)
		if phase == "Succeeded" || phase == "Failed" {
			return phase, nil
		}
		if time.Now().After(deadline) {
			return phase, fmt.Errorf("pod %s did not reach a terminal phase within %s (last phase %q)", podName, timeout, phase)
		}
		time.Sleep(3 * time.Second)
	}
}

// podManifest renders a one-shot talosctl Pod. When secretName is non-empty, that Secret
// (already created in the same namespace) is mounted read-only at /secrets.
func podManifest(podName, namespace string, args []string, secretName string) string {
	cmdJSON, _ := json.Marshal(append([]string{"/talosctl"}, args...))

	var mounts, volumes string
	if secretName != "" {
		mounts = "\n      volumeMounts:\n        - name: secrets\n          mountPath: /secrets\n          readOnly: true"
		volumes = fmt.Sprintf("\n  volumes:\n    - name: secrets\n      secret:\n        secretName: %s", secretName)
	}

	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  restartPolicy: Never
  containers:
    - name: talosctl
      image: %s
      command: %s%s%s
`, podName, namespace, Image, cmdJSON, mounts, volumes)
}

// applySecret creates an Opaque Secret named name in namespace with one base64-encoded data
// entry per key in data.
func applySecret(namespace, name string, data map[string]string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\ndata:\n", name, namespace)
	for k, v := range data {
		fmt.Fprintf(&b, "  %s: %s\n", k, base64.StdEncoding.EncodeToString([]byte(v)))
	}
	// create, not apply: apply copies the manifest into a last-applied annotation, which a large
	// machine config (a Calico inline manifest) pushes over the 256 KiB annotation limit.
	cmd := kube.Command("create", "-f", "-")
	cmd.Stdin = strings.NewReader(b.String())
	_, err := utils.Run(cmd)
	return err
}

func deleteSecret(namespace, name string) {
	_, _ = utils.Run(kube.Command("-n", namespace, "delete", "secret", name, "--wait=false", "--ignore-not-found"))
}

// Reset wipes STATE and EPHEMERAL on node's authenticated API and reboots it, without the graceful
// leave, so a node of a cluster the caller owns comes back in maintenance mode (ADR 0103 harness).
func Reset(namespace, node string, talosconfig []byte) (string, error) {
	return run(namespace,
		[]string{"reset", "--graceful=false", "--reboot", "--wait=false",
			"--system-labels-to-wipe", "STATE", "--system-labels-to-wipe", "EPHEMERAL",
			talosconfigFlag, talosconfigMount, nodesFlag, node, "--endpoints", node},
		map[string]string{talosconfigKey: string(talosconfig)}, 2*time.Minute)
}
