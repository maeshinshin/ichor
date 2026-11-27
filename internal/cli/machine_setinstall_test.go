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
	"testing"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// approvedMachine is strongMachine approved without an install disk: Ready=False/InstallDiskUnknown.
const setDiskWWID = "wwid:naa.7"

func approvedMachine() *ichorv1alpha1.TalosMachine {
	m := strongMachine()
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved

	return m
}

func runSetInstallDisk(t *testing.T, objs []client.Object, opts setInstallDiskOptions) (client.Client, error) {
	t.Helper()
	setAPITestFlags(t, true)

	cluster := newTestCluster()
	cluster.Name = strongMachine().Spec.ClusterRef.Name
	c, k8s := freshClients(t, append(objs, cluster))

	return k8s, runSetInstallDiskCmd(context.Background(), c, &bytes.Buffer{}, "strong", opts)
}

func TestSetInstallDiskSetsTheDiskOfAnInstallDiskUnknownMachine(t *testing.T) {
	k8s, err := runSetInstallDisk(t, []client.Object{approvedMachine()},
		setInstallDiskOptions{Disk: setDiskWWID, Image: "factory.talos.dev/set:v1"})
	if err != nil {
		t.Fatal(err)
	}

	got := getMachine(t, k8s, "strong")
	if got.Spec.Install == nil || got.Spec.Install.Disk.WWID != "naa.7" || got.Spec.Install.Image != "factory.talos.dev/set:v1" {
		t.Fatalf("want spec.install {wwid naa.7, image}, got %+v", got.Spec.Install)
	}
}

func TestSetInstallDiskRefusesAConvergedMachine(t *testing.T) {
	m := approvedMachine()
	m.Status.AppliedConfigHash = "abc"

	k8s, err := runSetInstallDisk(t, []client.Object{m}, setInstallDiskOptions{Disk: setDiskWWID})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || getMachine(t, k8s, "strong").Spec.Install != nil {
		t.Fatalf("a converged machine must be refused untouched (err=%v)", err)
	}
}

func TestSetInstallDiskRefusesAnImageWithoutADisk(t *testing.T) {
	k8s, err := runSetInstallDisk(t, []client.Object{approvedMachine()}, setInstallDiskOptions{Image: "x"})
	if err == nil || getMachine(t, k8s, "strong").Spec.Install != nil {
		t.Fatalf("--install-image without --install-disk must be refused (err=%v)", err)
	}
}

func TestSetInstallDiskRefusesAMachineThatIsNotApproved(t *testing.T) {
	k8s, err := runSetInstallDisk(t, []client.Object{strongMachine()}, setInstallDiskOptions{Disk: setDiskWWID})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || getMachine(t, k8s, "strong").Spec.Install != nil {
		t.Fatalf("an unapproved machine must be refused untouched (err=%v)", err)
	}
}
