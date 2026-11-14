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

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestParseInstallDisk(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ichorv1alpha1.TalosMachineDisk
	}{
		{"wwid:naa.5000c500", ichorv1alpha1.TalosMachineDisk{WWID: "naa.5000c500"}},
		{"serial:S3Z9", ichorv1alpha1.TalosMachineDisk{Serial: "S3Z9"}},
		{"/dev/disk/by-id/nvme-X", ichorv1alpha1.TalosMachineDisk{ByIDPath: "/dev/disk/by-id/nvme-X"}},
		{testInstallDisk, ichorv1alpha1.TalosMachineDisk{DevPath: testInstallDisk}},
		{"/pci0000:00/0000:00:17.0/ata1", ichorv1alpha1.TalosMachineDisk{BusPath: "/pci0000:00/0000:00:17.0/ata1"}},
	} {
		got, err := parseDiskSelector("--install-disk", tc.in)
		if err != nil || *got != tc.want {
			t.Errorf("parseDiskSelector(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}

	for _, bad := range []string{"", "sda", "wwid:", "serial:", "vda"} {
		if _, err := parseDiskSelector("--install-disk", bad); err == nil {
			t.Errorf("parseDiskSelector(%q) must fail", bad)
		}
	}
}

func TestApproveWritesTheInstallDisk(t *testing.T) {
	setAPITestFlags(t, true)

	c, k8s := freshClients(t, []client.Object{strongMachine()})

	// A flag the CLI cannot parse is refused before anything is written.
	err := runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong",
		machineApproveOptions{Role: testRoleWorker, InstallDisk: "sda"})
	if err == nil || getMachine(t, k8s, "strong").Spec.IsApproved() {
		t.Fatalf("a malformed --install-disk must not approve (err=%v)", err)
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong",
		machineApproveOptions{Role: testRoleWorker, InstallImage: "x"})
	if err == nil {
		t.Fatal("--install-image without --install-disk must be refused")
	}

	// The backend's hostname check reaches the caller as InvalidArgument.
	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong",
		machineApproveOptions{Role: testRoleWorker, Hostname: "てすと"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a non-RFC 1123 hostname: want InvalidArgument, got %v", err)
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong",
		machineApproveOptions{Role: testRoleWorker, InstallDisk: "wwid:naa.1", InstallImage: "factory.talos.dev/i:v1"})
	if err != nil {
		t.Fatal(err)
	}

	got := getMachine(t, k8s, "strong")
	if got.Spec.Install == nil || got.Spec.Install.Disk.WWID != "naa.1" || got.Spec.Install.Image != "factory.talos.dev/i:v1" {
		t.Fatalf("want spec.install {wwid naa.1, image}, got %+v", got.Spec.Install)
	}
}

// TestApproveWarnsAboutAnUndiscoveredDisk: an install disk discovery did not record is printed as a
// warning on stderr and the approval still goes through.
func TestApproveWarnsAboutAnUndiscoveredDisk(t *testing.T) {
	setAPITestFlags(t, true)

	m := strongMachine()
	m.Status.DiscoveredDisks = []ichorv1alpha1.TalosMachineDiscoveredDisk{{WWID: "naa.2"}}

	// An approval with an install disk names the installer of the machine's cluster, so the cluster must exist.
	cluster := newTestCluster()
	cluster.Name = m.Spec.ClusterRef.Name

	c, k8s := freshClients(t, []client.Object{m, cluster})

	var errw bytes.Buffer
	if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw, "strong",
		machineApproveOptions{Role: testRoleWorker, InstallDisk: "wwid:naa.9"}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errw.String(), "warning: install disk") {
		t.Fatalf("want an install disk warning on stderr, got %q", errw.String())
	}

	if !getMachine(t, k8s, "strong").Spec.IsApproved() {
		t.Fatal("a warning must not stop the approval")
	}
}
