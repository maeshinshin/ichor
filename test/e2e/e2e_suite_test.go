//go:build e2e
// +build e2e

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

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// talosNodesEnvVar names the comma-separated list of Talos node addresses the suite reads
// its node to probe from. Neither harness provisions nodes itself: the KubeVirt
// harness's "setup-test-e2e-kubevirt" and the talosctl-qemu harness's "setup-test-e2e" each
// create one their own way and publish it here, so one set of specs runs unchanged against
// either.
const talosNodesEnvVar = "ICHOR_E2E_TALOS_NODES"

// innerKubeconfigEnvVar names the path to the kubeconfig for the inner cluster the CRD and
// CEL specs must run against, not the (possibly production) cluster KUBECONFIG points at
// (P2-8): the harness that created talosNode() also stood up a cluster on it.
const innerKubeconfigEnvVar = "ICHOR_E2E_INNER_KUBECONFIG"

// talosconfigEnvVar names the path to the admin talosconfig for talosNode()'s authenticated
// API, published alongside innerKubeconfigEnvVar once the harness has applied a
// configuration (P2-8).
const talosconfigEnvVar = "ICHOR_E2E_TALOSCONFIG"

// talosNodes holds the parsed node addresses, set once in BeforeSuite.
var talosNodes []string

// innerKubeconfig and talosconfig hold the paths read from innerKubeconfigEnvVar and
// talosconfigEnvVar, and talosconfigBytes their contents, all set once in BeforeSuite.
var (
	innerKubeconfig  string
	talosconfig      string
	talosconfigBytes []byte
)

// talosNode is the node this run's specs probe.
func talosNode() string {
	return talosNodes[0]
}

// TestE2E runs the e2e test suite against the cluster given by KUBECONFIG, and the Talos
// node(s) given by ICHOR_E2E_TALOS_NODES. The suite does not create or tear
// down either itself, and does not build or load a manager image - phase 1's e2e proves
// the CRDs and CEL rules against a real API server and a real Talos node, nothing else.
//
// To enable kubectl kuberc (use custom kubectl configurations), set: KUBECTL_KUBERC=true
// By default, kuberc is disabled to ensure consistent test behavior across different environments.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting ichor e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	configureKubectlKubeRC()

	raw := requireEnv(talosNodesEnvVar, "its maintenance-mode node endpoint(s)")
	for _, n := range strings.Split(raw, ",") {
		if n = strings.TrimSpace(n); n != "" {
			talosNodes = append(talosNodes, n)
		}
	}

	innerKubeconfig = requireEnv(innerKubeconfigEnvVar, "the inner cluster's kubeconfig")
	talosconfig = requireEnv(talosconfigEnvVar, "an admin talosconfig for the node")
	var err error
	talosconfigBytes, err = os.ReadFile(talosconfig)
	Expect(err).NotTo(HaveOccurred(), "could not read %s=%s", talosconfigEnvVar, talosconfig)
})

// requireEnv returns the named variable, failing the suite when the harness did not publish it.
func requireEnv(name, what string) string {
	v := os.Getenv(name)
	if strings.TrimSpace(v) == "" {
		Fail(fmt.Sprintf("%s is not set. The harness's setup target must publish %s through it "+
			"before the suite runs.", name, what))
	}
	return v
}

// Disable kubectl kuberc by default for test isolation.
// This prevents local kubectl configurations from affecting test behavior.
// To enable kuberc, set: KUBECTL_KUBERC=true
func configureKubectlKubeRC() {
	if os.Getenv("KUBECTL_KUBERC") != "true" {
		By("disabling kubectl kuberc for test isolation")
		err := os.Setenv("KUBECTL_KUBERC", "false")
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to disable kubectl kuberc")
		_, _ = fmt.Fprintf(GinkgoWriter,
			"kubectl kuberc disabled for consistent test behavior (override with KUBECTL_KUBERC=true)\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "kubectl kuberc enabled (KUBECTL_KUBERC=true)\n")
	}
}
