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
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/installmanifest"
)

// TestRunInit_DryRunWritesNothing pins the dry run's one contract: it reads the first node, then stops
// before the first write. An apply, a bootstrap, a Kubernetes client (which the Gateway API CRDs and
// the install manifest go through), a bundle file under ~/.ichor or a kubeconfig would each fail it.
func TestRunInit_DryRunWritesNothing(t *testing.T) {
	home := withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, boot := newFakeInitDeps(t, k8sClient)

	connected := false
	newClient := deps.NewK8sClient
	deps.NewK8sClient = func(kubeconfig []byte) (client.Client, error) {
		connected = true

		return newClient(kubeconfig)
	}

	opts := validInitOptions()
	opts.DryRun = true

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	if maint.callCount != 0 || boot.bootstrapCalled || connected {
		t.Fatalf("dry run reached the node or the cluster: applies=%d bootstrap=%v kubernetes client=%v",
			maint.callCount, boot.bootstrapCalled, connected)
	}

	if _, err := os.Stat(home + "/.ichor"); !os.IsNotExist(err) {
		t.Fatalf("dry run left files under ~/.ichor: stat err %v", err)
	}
}

// TestRunInit_DryRunReportsThePlan checks the plan names the node it read, the disk the selector matched
// (with its model and serial) and the endpoint, says nothing was written, and never prints the bundle
// passphrase.
func TestRunInit_DryRunReportsThePlan(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, _, _ := newFakeInitDeps(t, k8sClient)

	deps.ReadNode = func(context.Context, string, bool) (nodeReading, error) {
		return nodeReading{
			Identity:     controller.HardwareIdentity{UUID: testInitMachineUUID, Serial: "dry-run-serial"},
			TalosVersion: testTalosVersion,
			Disks:        []ichorv1alpha1.TalosMachineDiscoveredDisk{{DevPath: testInstallDisk, Model: "TEST-DISK", Serial: "DISK-SN-1"}},
			Links:        []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink},
		}, nil
	}

	opts := validInitOptions()
	opts.DryRun = true
	opts.BundlePlaintext = false
	opts.BundlePassphrase = "dry-run-passphrase-must-not-print"

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	out := deps.Stdout.(*bytes.Buffer).String()
	for _, want := range []string{testInitClusterName, testInitMachineUUID, testEndpointAddr, "TEST-DISK", "DISK-SN-1", "running Talos " + testTalosVersion, "nothing is written"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}

	if all := out + deps.Stderr.(*bytes.Buffer).String(); strings.Contains(all, opts.BundlePassphrase) {
		t.Errorf("dry run printed the bundle passphrase")
	}
}

// TestRunInit_DryRunRefusesAnOlderTalos is the refusal of an older node, as the real run makes it, with the same
// promise: nothing is applied to the node.
func TestRunInit_DryRunRefusesAnOlderTalos(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, _ := newFakeInitDeps(t, k8sClient)
	read := deps.ReadNode
	deps.ReadNode = func(ctx context.Context, endpoint string, wantInventory bool) (nodeReading, error) {
		n, err := read(ctx, endpoint, wantInventory)
		n.TalosVersion = "v1.13.1"

		return n, err
	}

	opts := validInitOptions()
	opts.DryRun = true

	err := runInit(context.Background(), opts, deps)
	if err == nil || !strings.Contains(err.Error(), "runs Talos v1.13.1") {
		t.Fatalf("runInit = %v, want the refusal of a node on Talos v1.13.1", err)
	}

	if maint.callCount != 0 {
		t.Fatalf("the refusal came after %d apply(s) to the node", maint.callCount)
	}
}

// TestRunInit_DryRunRefusesAnUnembeddedBinary: a dry run refuses what the real run would refuse, here a
// binary built without its install manifest, before the node is configured rather than after.
func TestRunInit_DryRunRefusesAnUnembeddedBinary(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, maint, _ := newFakeInitDeps(t, k8sClient)
	deps.IchorManifest = []byte(installmanifest.PlaceholderSentinel)

	opts := validInitOptions()
	opts.DryRun = true

	err := runInit(context.Background(), opts, deps)
	if err == nil || !strings.Contains(err.Error(), "no install manifest embedded") {
		t.Fatalf("runInit = %v, want the refusal for a binary with no install manifest", err)
	}

	if maint.callCount != 0 {
		t.Fatalf("the refusal came after %d apply(s) to the node; a dry run must refuse before the first one", maint.callCount)
	}
}
