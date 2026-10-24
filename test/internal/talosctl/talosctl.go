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

// Package talosctl runs talosctl against a Talos node's insecure maintenance API from a
// one-shot Pod inside the cluster.
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
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/maeshinshin/ichor/test/utils"
)

// Image is the talosctl release this package runs; it must match the Talos version the
// e2e nodes boot.
const Image = "ghcr.io/siderolabs/talosctl:v1.14.3"

const podManifestTmpl = `apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  restartPolicy: Never
  containers:
    - name: talosctl
      image: %[3]s
      command: ["/talosctl", "get", "%[4]s", "-o", "json", "--insecure", "--nodes", "%[5]s"]
`

// Get runs `talosctl get <resource> -o json --insecure --nodes <node>` against node from a
// one-shot Pod in namespace, and returns its raw (possibly multi-object) JSON output.
func Get(namespace, node, resource string) (string, error) {
	podName := fmt.Sprintf("talosctl-%s-%d", resource, time.Now().UnixNano())
	manifest := fmt.Sprintf(podManifestTmpl, podName, namespace, Image, resource, node)
	defer func() {
		_, _ = utils.Run(exec.Command("kubectl", "-n", namespace, "delete", "pod", podName,
			"--wait=false", "--ignore-not-found"))
	}()

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if _, err := utils.Run(cmd); err != nil {
		return "", err
	}
	if _, err := utils.Run(exec.Command("kubectl", "-n", namespace, "wait", "pod/"+podName,
		"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s")); err != nil {
		return "", err
	}
	return utils.Run(exec.Command("kubectl", "-n", namespace, "logs", podName))
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

// MetadataID reads a decoded resource's "metadata.id".
func MetadataID(obj map[string]any) any {
	metadata, _ := obj["metadata"].(map[string]any)
	return metadata["id"]
}
