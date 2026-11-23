package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// A member lists every address of the node, Cilium's cluster-pool cilium_host address among them (10.0.0.0/8 sorts
// first). Adopt must dial and record the seed address for the seed's member and the Node's InternalIP for the others.
func TestDiscoverMachinesPicksNodeAddressesNotCiliumHostOnes(t *testing.T) {
	const cpSeed, wkIP, cpHost, wkHost = "10.244.2.87", "10.244.2.88", "10.0.0.187", "10.0.1.12"

	node := func(name, ip string) *corev1.Node {
		return &corev1.Node{
			Name:   name,
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}},
		}
	}

	deps := adoptDeps{Seed: cpSeed, Target: fake.NewClientBuilder().WithObjects(node("cp", cpSeed), node("wk", wkIP)).Build()}
	deps.DialTalos = func(context.Context, string) (talosConfigReader, error) {
		return &membersReader{members: []ClusterMember{
			{Hostname: "cp", Addresses: []string{cpHost, cpSeed}, Role: roleControlPlane},
			{Hostname: "wk", Addresses: []string{wkHost, wkIP}, Role: testRoleWorker},
		}}, nil
	}
	deps.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "u-" + address}, nil
	}

	machines, _, err := discoverMachines(context.Background(), deps, "c", "ns")
	if err != nil {
		t.Fatal(err)
	}

	if got := []string{machines[0].Spec.Endpoint, machines[1].Spec.Endpoint}; got[0] != cpSeed || got[1] != wkIP {
		t.Fatalf("endpoints %v; want [%s %s]", got, cpSeed, wkIP)
	}
}

// A Cilium on ipam.mode cluster-pool is refused whatever the nodes' addresses are: the refusal reads only the
// Kubernetes API, so it comes before adopt dials any member (a worker's Node address may sit in the pod pool).
func TestDiscoverMachinesRefusesClusterPoolBeforeDialingMembers(t *testing.T) {
	const cpSeed, wkHost = "10.244.2.87", "10.0.1.12"

	node := func(name, ip string) *corev1.Node {
		return &corev1.Node{
			Name:   name,
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}},
		}
	}

	var identityReads int

	deps := adoptDeps{
		Seed: cpSeed,
		Target: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
			node("cp", cpSeed), node("wk", wkHost), testDS("cilium", ciliumImage), ciliumCM("false")).Build(),
		Preflight: func(_ context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
			rep := &cilium.PreflightReport{}
			if in.ConflictsOnly {
				rep.Conflicts = []cilium.Conflict{{Key: "mode", Live: "cluster-pool", Level: cilium.Blocking}}
			}

			return rep, nil
		},
		DialTalos: func(context.Context, string) (talosConfigReader, error) {
			return &membersReader{members: []ClusterMember{
				{Hostname: "cp", Addresses: []string{cpSeed}, Role: roleControlPlane},
				{Hostname: "wk", Addresses: []string{wkHost}, Role: testRoleWorker},
			}}, nil
		},
		ReadIdentity: func(context.Context, string) (controller.HardwareIdentity, error) {
			identityReads++

			return controller.HardwareIdentity{}, errors.New("i/o timeout")
		},
	}

	_, _, err := discoverMachines(context.Background(), deps, "c", "ns")
	if err == nil || !strings.Contains(err.Error(), "cannot be taken over") {
		t.Fatalf("err = %v; want the ipam.mode refusal", err)
	}

	if identityReads != 0 {
		t.Errorf("adopt read %d members' hardware identity before refusing", identityReads)
	}
}
