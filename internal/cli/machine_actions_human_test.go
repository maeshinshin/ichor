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
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// These tests pin the human output of the commands that change one thing on a pipe,
// so with no colour codes, and that -o yaml and -o json still print what they printed before.

const (
	actionsMachineUUID  = "fa70962a-5a5e-4b9c-9d39-0d3a9c1b2e10"
	actionsMachineShort = "fa70962a"
	actionsMachineIP    = "10.0.0.10"
	actionsRangeIP      = "10.1.0.0/24"
	actionsNewAddress   = "10.0.0.7"
	actionsNewCluster   = "new"
	// actionsOtherMachine is a second, pending machine.
	actionsOtherMachine = "other-machine"
)

func pendingActionsMachine() *ichorv1alpha1.TalosMachine {
	m := strongMachine()
	m.Name = actionsMachineUUID
	m.Spec.ClusterRef.Name = humanCluster
	m.Spec.Endpoint = actionsMachineIP + ":50000"

	return m
}

func TestMachineApproveHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(pendingActionsMachine()).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out, errw bytes.Buffer
	if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &out, &errw, actionsMachineUUID,
		machineApproveOptions{Role: roleControlPlane, Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	if want := "✓ approved " + actionsMachineShort + " as control-plane in " + humanCluster + "\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}

	if errw.Len() != 0 {
		t.Errorf("stderr: %q", errw.String())
	}
}

// A pick discovery did not record is advice: a ! line under the ✓ line, not a refusal.
func TestMachineApproveHumanOutputShowsTheWarningsAsBangLines(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	m := pendingActionsMachine()
	m.Status.DiscoveredDisks = []ichorv1alpha1.TalosMachineDiscoveredDisk{{WWID: "naa.2"}}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(m).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out, errw bytes.Buffer
	if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &out, &errw, actionsMachineUUID,
		machineApproveOptions{Role: testRoleWorker, InstallDisk: "wwid:naa.9"}); err != nil {
		t.Fatal(err)
	}

	if want := "✓ approved " + actionsMachineShort + " as worker in " + humanCluster + "\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}

	if !strings.HasPrefix(errw.String(), "! install disk ") || strings.Contains(errw.String(), "warning:") {
		t.Errorf("want a ! line for the install disk, got %q", errw.String())
	}

	t.Logf("\n%s%s", out.String(), errw.String())
}

func TestMachineRejectHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(pendingActionsMachine()).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out bytes.Buffer
	if err := runMachineReject(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, actionsMachineUUID, "not ours"); err != nil {
		t.Fatal(err)
	}

	if want := "✓ rejected " + actionsMachineShort + ": not ours\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

// -o yaml and -o json print the wire message, no ✓ line.
func TestMachineActionsMachineFormatsAreUnchanged(t *testing.T) {
	for _, format := range []string{outputYAML, outputJSON} {
		t.Run(format, func(t *testing.T) {
			setHumanTestFlags(t, format)

			other := strongMachine()
			other.Name = actionsOtherMachine

			k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(pendingActionsMachine(), other).Build()
			c := localClients(apiOpts, apiDeps{K8s: k8s})

			var out bytes.Buffer
			if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, actionsMachineUUID, approveWorker); err != nil {
				t.Fatal(err)
			}

			if strings.Contains(out.String(), "✓") || !strings.Contains(out.String(), "MACHINE_APPROVAL_STATE_APPROVED") {
				t.Errorf("approve -o %s:\n%s", format, out.String())
			}

			out.Reset()

			if err := runMachineReject(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, actionsOtherMachine, "x"); err != nil {
				t.Fatal(err)
			}

			if strings.Contains(out.String(), "✓") || !strings.Contains(out.String(), "audit") {
				t.Errorf("reject -o %s:\n%s", format, out.String())
			}
		})
	}
}

func TestMachineReclaimHumanOutputOnAPlainWriter(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{})

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Wait: time.Second, Cluster: actionsNewCluster}); err != nil {
		t.Fatal(err)
	}

	out := h.out.String()

	t.Logf("\n%s", out)

	if strings.Contains(out, "\x1b") || strings.Contains(out, "\r") {
		t.Errorf("escape sequences on a writer that is no terminal: %q", out)
	}

	want := []string{
		"w1 · 10.0.0.5\n",
		"  Role         worker · Talos ",
		"  UUID         AAAA-1\n",
		"  Cluster      old (id cid), reachable from the node\n",
		"  Reset        STATE and EPHEMERAL, then reboot · graceful\n",
		"/dev/sdb1 (10 bytes), not wiped  ! ENCRYPTED: its key may depend on STATE",
		"This wipes STATE and EPHEMERAL on w1 and removes it from cluster \"old\".\nType the machine's UUID to continue: ",
		"✓ Reset — accepted, graceful\n",
		"✓ Maintenance mode — 10.0.0.5 answers with UUID AAAA-1\n",
		"✓ Register — added to cluster new's discovery addresses; it will appear as a pending machine\n",
		doneInPrefix,
		"Next\n  ichor machine list",
	}

	rest := out
	for _, w := range want {
		i := strings.Index(rest, w)
		if i < 0 {
			t.Fatalf("%q is missing, or out of order, in:\n%s", w, out)
		}

		rest = rest[i+len(w):]
	}
}

func TestMachineReclaimARefusalIsAFailedLineAndTheHintStaysVisible(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{MachineUUIDs: []string{strings.ToLower(testUUID)}})

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID}); err == nil {
		t.Fatal("want a refusal")
	}

	if !strings.Contains(h.out.String(), "✗ refused: node AAAA-1 is already an Ichor machine") {
		t.Errorf("the refusal is not shown:\n%s", h.out.String())
	}

	h = newReclaimHarness("AAAA-1\n", reclaim.Known{})
	h.deps.Probe = func(context.Context, string) (string, error) { return "", context.DeadlineExceeded }

	err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Wait: 20 * time.Millisecond, Cluster: actionsNewCluster})
	if err == nil || !errorShown(err) {
		t.Fatalf("want an error a step already showed, got %v", err)
	}

	out := h.out.String()
	for _, want := range []string{"✗ Maintenance mode — failed\n", "new address from DHCP", "its UUID is AAAA-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func discoveryHumanCluster(t *testing.T) *apiClients {
	t.Helper()
	setHumanTestFlags(t, outputHuman)

	cl := newTestCluster()
	cl.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{
		Addresses: []string{testEndpointAddr, actionsMachineIP}, CIDRs: []string{actionsRangeIP, "10.0.0.0/24"},
	}

	m := newTestMachine(cl.Name)
	m.Name = actionsMachineUUID

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cl, m).Build()

	return localClients(apiOpts, apiDeps{K8s: k8s})
}

func TestClusterDiscoveryListHumanOutput(t *testing.T) {
	c := discoveryHumanCluster(t)

	var out bytes.Buffer
	if err := runDiscoveryList(context.Background(), c, &out); err != nil {
		t.Fatal(err)
	}

	want := "ADDRESS/CIDR  KIND     FOUND\n" +
		"10.0.0.5      address  -\n" +
		"10.0.0.10     address  " + actionsMachineShort + "\n" +
		"10.1.0.0/24   range    -\n" +
		"10.0.0.0/24   range    " + actionsMachineShort + "\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestClusterDiscoveryEditHumanOutput(t *testing.T) {
	c := discoveryHumanCluster(t)
	cmd := discoveryCmdIO("")

	for target, want := range map[string]string{
		actionsNewAddress: "✓ added " + actionsNewAddress + " to the discovery of adopt-cluster\n",
		"192.168.0.9/24":  "✓ added 192.168.0.9/24 to the discovery of adopt-cluster\n",
	} {
		if err := runDiscoveryEdit(context.Background(), c, cmd, target, false); err != nil {
			t.Fatal(err)
		}

		if got := cmd.OutOrStdout().(*bytes.Buffer).String(); got != want { //nolint:errcheck,forcetypeassert // discoveryCmdIO sets a Buffer
			t.Errorf("got %q, want %q", got, want)
		}

		cmd.OutOrStdout().(*bytes.Buffer).Reset() //nolint:errcheck,forcetypeassert // discoveryCmdIO sets a Buffer
	}

	if err := runDiscoveryEdit(context.Background(), c, cmd, actionsRangeIP, true); err != nil {
		t.Fatal(err)
	}

	if got, want := cmd.OutOrStdout().(*bytes.Buffer).String(), "✓ removed "+actionsRangeIP+" from the discovery of adopt-cluster\n"; got != want { //nolint:errcheck,forcetypeassert // see above
		t.Errorf("got %q, want %q", got, want)
	}
}

// An address that already runs a Talos configuration is not added as a machine: that is a ! line.
func TestClusterDiscoveryAddWarnsAboutAnAlreadyConfiguredAddress(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := printDiscoveryEdit(&out, &pb.AddDiscoveryAddressResponse{}, false, testEndpointAddr, objectRef(testNamespace, humanCluster),
		[]string{testEndpointAddr}); err != nil {
		t.Fatal(err)
	}

	want := "✓ added 10.0.0.5 to the discovery of initshow\n" +
		"! 10.0.0.5 already runs a Talos configuration, so discovery does not add it as a machine; " +
		"if it is a member of this cluster's Talos cluster run `ichor adopt --cluster initshow --add-machine 10.0.0.5`, " +
		"if another cluster configured it run `ichor machine reclaim 10.0.0.5 --talosconfig FILE --cluster initshow`\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// The machine formats print the wire message and the lines discovery list always printed.
func TestClusterDiscoveryMachineFormatsAreUnchanged(t *testing.T) {
	for _, format := range []string{outputYAML, outputJSON} {
		setHumanTestFlags(t, format)

		msg := &pb.AddDiscoveryCidrResponse{Cluster: &pb.Cluster{Ref: objectRef(testNamespace, humanCluster)}}

		var got, want bytes.Buffer
		if err := printDiscoveryEdit(&got, msg, false, actionsRangeIP, objectRef(testNamespace, humanCluster), nil); err != nil {
			t.Fatal(err)
		}

		if err := writeMessage(&want, format, msg); err != nil {
			t.Fatal(err)
		}

		if got.String() != want.String() {
			t.Errorf("-o %s: got %q, want %q", format, got.String(), want.String())
		}
	}

	c := discoveryHumanCluster(t)
	outputFormat = outputYAML

	var out bytes.Buffer
	if err := runDiscoveryList(context.Background(), c, &out); err != nil {
		t.Fatal(err)
	}

	if want := "address  10.0.0.5\naddress  10.0.0.10\nrange    10.1.0.0/24\nrange    10.0.0.0/24\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestRotateCAHumanOutput(t *testing.T) {
	const newKubeconfig = "/home/op/.kube/config.ichor-new"

	var out bytes.Buffer
	if err := printRotateCAStarted(&out, "ca-rotation-x7k2p", opTestCluster, newKubeconfig); err != nil {
		t.Fatal(err)
	}

	want := "✓ started ca-rotation operation ca-rotation-x7k2p\n" +
		"  follow with: ichor operation get ca-rotation-x7k2p --watch --kubeconfig " + newKubeconfig + "\n\n" +
		"When it completes\n" +
		"  ichor cluster credentials " + opTestCluster + " --kubeconfig " + newKubeconfig + "   files that trust the new CA only\n" +
		"  ichor cluster bundle-export " + opTestCluster + "   save the new secrets bundle\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()

	if err := printRotateCADone(&out, "ca-rotation-x7k2p", opTestCluster, newKubeconfig); err != nil {
		t.Fatal(err)
	}

	if want := "✓ ca-rotation operation ca-rotation-x7k2p completed\n\nNext\n"; !strings.HasPrefix(out.String(), want) {
		t.Errorf("got:\n%s", out.String())
	}
}
