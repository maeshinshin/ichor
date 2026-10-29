package v1alpha1

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func reclaimObject(t *testing.T, confirm, requestedBy, confirmedBy string, phase ichorv1alpha1.MachineReclaimPhase, uuid string) runtime.RawExtension {
	t.Helper()

	mr := ichorv1alpha1.MachineReclaim{
		Name: "x", Namespace: "ichor-system", Annotations: map[string]string{},
		Spec:   ichorv1alpha1.MachineReclaimSpec{Address: "192.0.2.1", CredentialSecretRef: "s", ConfirmUUID: confirm},
		Status: ichorv1alpha1.MachineReclaimStatus{Phase: phase},
	}

	if requestedBy != "" {
		mr.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation] = requestedBy
	}

	if confirmedBy != "" {
		mr.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation] = confirmedBy
	}

	if uuid != "" {
		mr.Status.Inspection = &ichorv1alpha1.MachineReclaimInspection{UUID: uuid}
	}

	raw, err := json.Marshal(mr)
	if err != nil {
		t.Fatal(err)
	}

	return runtime.RawExtension{Raw: raw}
}

func reclaimReq(op admissionv1.Operation, user string, obj, old runtime.RawExtension) admission.Request {
	var req admission.Request

	req.Operation, req.Name, req.Namespace = op, "x", "ichor-system"
	req.UserInfo = authnv1.UserInfo{Username: user}
	req.Object, req.OldObject = obj, old

	return req
}

func TestMachineReclaimWebhook(t *testing.T) {
	t.Parallel()

	// bgUnitHandler answers SubjectAccessReviews: only bgUnitHolder holds the verb.
	h := &MachineReclaimHandler{Client: bgUnitHandler(t).Client}
	ctx := context.Background()
	awaiting := ichorv1alpha1.MachineReclaimAwaitingConfirmation
	stored := reclaimObject(t, "", bgUnitHolder, "", awaiting, "uuid-1")

	tests := []struct {
		name    string
		h       *MachineReclaimHandler
		req     admission.Request
		allowed bool
	}{
		{"create by a holder", h, reclaimReq(admissionv1.Create, bgUnitHolder,
			reclaimObject(t, "", bgUnitHolder, "", "", ""), runtime.RawExtension{}), true},
		{"create without the verb", h, reclaimReq(admissionv1.Create, bgUnitPlain,
			reclaimObject(t, "", bgUnitPlain, "", "", ""), runtime.RawExtension{}), false},
		{"create with a forged requester", h, reclaimReq(admissionv1.Create, bgUnitHolder,
			reclaimObject(t, "", "someone-else", "", "", ""), runtime.RawExtension{}), false},
		{"create with confirmUUID", h, reclaimReq(admissionv1.Create, bgUnitHolder,
			reclaimObject(t, "uuid-1", bgUnitHolder, "", "", ""), runtime.RawExtension{}), false},
		{"confirm with the right UUID", h, reclaimReq(admissionv1.Update, bgUnitHolder,
			reclaimObject(t, "uuid-1", bgUnitHolder, bgUnitHolder, awaiting, "uuid-1"), stored), true},
		{"confirm with a different UUID", h, reclaimReq(admissionv1.Update, bgUnitHolder,
			reclaimObject(t, "uuid-2", bgUnitHolder, bgUnitHolder, awaiting, "uuid-1"), stored), false},
		{"confirm without the verb", h, reclaimReq(admissionv1.Update, bgUnitPlain,
			reclaimObject(t, "uuid-1", bgUnitHolder, bgUnitPlain, awaiting, "uuid-1"), stored), false},
		{"confirm with a forged confirmer", h, reclaimReq(admissionv1.Update, bgUnitHolder,
			reclaimObject(t, "uuid-1", bgUnitHolder, "bob", awaiting, "uuid-1"), stored), false},
		{"confirm before inspection", h, reclaimReq(admissionv1.Update, bgUnitHolder,
			reclaimObject(t, "uuid-1", bgUnitHolder, bgUnitHolder, "", ""),
			reclaimObject(t, "", bgUnitHolder, "", "", "")), false},
		{"edit that leaves confirmUUID alone", h, reclaimReq(admissionv1.Update, bgUnitPlain,
			reclaimObject(t, "", bgUnitHolder, "", awaiting, "uuid-1"), stored), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.h.Handle(ctx, tt.req); got.Allowed != tt.allowed {
				t.Fatalf("allowed = %v, want %v (%v)", got.Allowed, tt.allowed, got.Result)
			}
		})
	}
}
