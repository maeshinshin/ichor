package uiapi

import (
	"context"
	"slices"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A machine that moved has its current address in status.observedEndpoint.
// Every Talos client the UI backend builds must dial that address, not spec.endpoint.
func TestTalosClientsDialObservedEndpoint(t *testing.T) {
	const specEndpoint = "10.0.0.10:50000"
	const observed = "10.0.0.77:50000"

	m := &ichorv1alpha1.TalosMachine{Spec: ichorv1alpha1.TalosMachineSpec{Endpoint: specEndpoint}}
	m.Status.ObservedEndpoint = observed
	m.Status.ObservedEndpointFor = specEndpoint

	cluster := &ichorv1alpha1.TalosCluster{}
	cluster.Name = "cluster"
	ctx := context.Background()

	maint, err := NewMachineryNodeTalosClient(ctx, cluster, m, nil)
	if err != nil {
		t.Fatalf("NewMachineryNodeTalosClient (maintenance): %v", err)
	}

	if got := maint.(*machineryNodeTalosClient).c.GetEndpoints(); !slices.Equal(got, []string{observed}) {
		t.Errorf("maintenance client endpoints = %v, want [%s]", got, observed)
	}

	reader, err := NewMachineryNodeTalosClient(ctx, cluster, m, testTalosIdentity(t))
	if err != nil {
		t.Fatalf("NewMachineryNodeTalosClient (reader): %v", err)
	}

	if got := reader.(*machineryNodeTalosClient).c.GetEndpoints(); !slices.Equal(got, []string{observed}) {
		t.Errorf("reader client endpoints = %v, want [%s]", got, observed)
	}

	download, err := NewMachineryDownloadTalosClient(ctx, cluster, m, testTalosIdentity(t), ScopeSupportBundle)
	if err != nil {
		t.Fatalf("NewMachineryDownloadTalosClient: %v", err)
	}

	if got := download.(*machineryDownloadClient).c.GetEndpoints(); !slices.Equal(got, []string{observed}) {
		t.Errorf("download client endpoints = %v, want [%s]", got, observed)
	}
}
