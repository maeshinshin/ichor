package cli

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/internal/controller"
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
