package cli

import (
	"context"
	"errors"
	"testing"

)

// A machine that moved has its current address in status.observedEndpoint.
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

	err := readNode(context.Background(), adoptDeps{DialTalos: dial}, &adoptNode{M: m}, false)
	if err == nil {
		t.Fatal("readNode: want the stubbed dial error")
	}

	if dialed != observed {
		t.Fatalf("adopt dialed %q, want the observed endpoint %q", dialed, observed)
	}
}
