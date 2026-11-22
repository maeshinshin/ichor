package cli

import (
	"context"
	"testing"

	"github.com/maeshinshin/ichor/internal/controller"
)

// A member lists every address of the node, Cilium's cluster-pool cilium_host address among them (it sorts first);
// adopt must dial and record the address it discovered the cluster through.
func TestDiscoverMachinesUsesTheSeedAddressOfTheSeedMember(t *testing.T) {
	const seed, ciliumHost = "10.244.2.87", "10.0.0.187"

	deps := adoptDeps{Seed: seed}
	deps.DialTalos = func(context.Context, string) (talosConfigReader, error) {
		return &membersReader{members: []ClusterMember{{Hostname: "cp", Addresses: []string{ciliumHost, seed}, Role: roleControlPlane}}}, nil
	}

	var read string

	deps.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		read = address

		return controller.HardwareIdentity{UUID: "u"}, nil
	}

	machines, _, err := discoverMachines(context.Background(), deps, "c", "ns")
	if err != nil {
		t.Fatal(err)
	}

	if read != seed || machines[0].Spec.Endpoint != seed {
		t.Fatalf("identity read at %q, endpoint %q; want %q", read, machines[0].Spec.Endpoint, seed)
	}
}
