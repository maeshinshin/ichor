package v1alpha1

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0108: the controller dials MachineReclaim.spec.address, so the addresses it must never reach
// (loopback, link-local, unspecified, multicast) are refused even for a holder of the reclaim verb.
func TestMachineReclaimWebhookRefusesForbiddenAddresses(t *testing.T) {
	t.Parallel()

	h := &MachineReclaimHandler{Client: bgUnitHandler(t).Client}

	for address, allowed := range map[string]bool{
		"192.0.2.1":            true,
		"192.0.2.1:50000":      true,
		"127.0.0.1":            false,
		"127.0.0.1:50000":      false,
		"169.254.169.254":      false,
		"0.0.0.0":              false,
		"224.0.0.1":            false,
		"[::1]:50000":          false,
		"[fe80::1]:50000":      false,
		"[::ffff:127.0.0.1]:1": false,
	} {
		var mr ichorv1alpha1.MachineReclaim

		raw := reclaimObject(t, "", bgUnitHolder, "", "", "")
		if err := json.Unmarshal(raw.Raw, &mr); err != nil {
			t.Fatal(err)
		}

		mr.Spec.Address = address

		b, err := json.Marshal(mr)
		if err != nil {
			t.Fatal(err)
		}

		got := h.Handle(context.Background(), reclaimReq(admissionv1.Create, bgUnitHolder, runtime.RawExtension{Raw: b}, runtime.RawExtension{}))
		if got.Allowed != allowed {
			t.Errorf("address %q: allowed = %v, want %v (%v)", address, got.Allowed, allowed, got.Result)
		}
	}
}
