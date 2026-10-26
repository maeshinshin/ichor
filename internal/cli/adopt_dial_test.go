package cli

import (
	"context"
	"errors"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A machine that moved (docs/adr/0086) has its current address in status.observedEndpoint.
// adopt must dial that address, not spec.endpoint.
func TestPlanMachineDialsObservedEndpoint(t *testing.T) {
	const specEndpoint = "10.0.0.10:50000"
	const observed = "10.0.0.77:50000"

	m := newTestMachine("cluster")
	m.Spec.Endpoint = specEndpoint
	m.Status.ObservedEndpoint = observed
	m.Status.ObservedEndpointFor = specEndpoint

	var dialed string

	dial := func(_ context.Context, endpoint string) (talosConfigReader, error) {
		dialed = endpoint

		return nil, errors.New("stop after dial")
	}

	_, err := planMachine(context.Background(), &ichorv1alpha1.TalosCluster{}, m, nil, nil, nil, dial)
	if err == nil {
		t.Fatal("planMachine: want the stubbed dial error")
	}

	if dialed != observed {
		t.Fatalf("adopt dialed %q, want the observed endpoint %q", dialed, observed)
	}
}
