package uiapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// TestReclaimServiceReadsSettings: spec.reclaim.enabled=false switches the API off even when the
// startup flag is on, with no restart (ADR 0105).
func TestReclaimServiceReadsSettings(t *testing.T) {
	off := false
	scheme := runtime.NewScheme()

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	inst := &ichorv1alpha1.IchorInstallation{
		Name: settings.SingletonName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Reclaim: &ichorv1alpha1.IchorReclaimSettings{Enabled: &off}},
	}
	s := &Server{
		ReclaimEnabled: true,
		Settings: &settings.Reader{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).Build(), Fallback: settings.Fallback{ReclaimEnabled: true},
		},
	}

	resp, err := s.CanReclaim(context.Background(), connect.NewRequest(&pb.CanReclaimRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Msg.Enabled {
		t.Fatal("spec off must beat the flag")
	}

	if err := s.reclaimGate(context.Background()); err == nil {
		t.Fatal("the gate must refuse")
	}
}
