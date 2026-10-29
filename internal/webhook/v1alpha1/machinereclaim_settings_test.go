package v1alpha1

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// TestMachineReclaimWebhookReadsSettings: the switch is read per call from the IchorInstallation
// (ADR 0105); a set spec field wins over the flag, an unset one falls back to it.
func TestMachineReclaimWebhookReadsSettings(t *testing.T) {
	t.Parallel()

	on, off := true, false
	scheme := runtime.NewScheme()

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	req := reclaimReq(admissionv1.Create, bgUnitHolder, reclaimObject(t, "", bgUnitHolder, "", "", ""), runtime.RawExtension{})
	base := bgUnitHandler(t).Client

	for name, tt := range map[string]struct {
		spec     *bool
		flag     bool
		readOnly bool
		want     bool
	}{
		"spec on beats flag off":       {&on, false, false, true},
		"spec off beats flag on":       {&off, true, false, false},
		"unset falls back to flag on":  {nil, true, false, true},
		"unset falls back to flag off": {nil, false, false, false},
		"read-only ignores spec on":    {&on, false, true, false},
		"read-only ignores spec off":   {&off, true, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inst := &ichorv1alpha1.IchorInstallation{ObjectMeta: metav1.ObjectMeta{Name: "ichor"}}
			if tt.spec != nil {
				inst.Spec.Reclaim = &ichorv1alpha1.IchorReclaimSettings{Enabled: tt.spec}
			}

			h := &MachineReclaimHandler{Client: base, Settings: &settings.Reader{
				Client:   fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).Build(),
				Fallback: settings.Fallback{ReclaimEnabled: tt.flag}, ReadOnly: tt.readOnly,
			}}

			if got := h.Handle(context.Background(), req).Allowed; got != tt.want {
				t.Fatalf("allowed = %v, want %v", got, tt.want)
			}
		})
	}
}
