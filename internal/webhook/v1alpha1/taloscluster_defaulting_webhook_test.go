package v1alpha1

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// TestTalosClusterDefaulter: spec.defaults.driftPolicy fills a new cluster's unset driftPolicy, at
// create only, and never overrides a value the user chose.
func TestTalosClusterDefaulter(t *testing.T) {
	t.Parallel()

	scheme := schemeOf(t)

	inst := &ichorv1alpha1.IchorInstallation{
		Name: settings.SingletonName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Defaults: &ichorv1alpha1.IchorDefaultSettings{DriftPolicy: "autoCorrect"}},
	}
	reader := func(readOnly bool, objs ...*ichorv1alpha1.IchorInstallation) *settings.Reader {
		b := fake.NewClientBuilder().WithScheme(scheme)
		for _, o := range objs {
			b = b.WithObjects(o)
		}

		return &settings.Reader{Client: b.Build(), ReadOnly: readOnly}
	}
	ctxOf := func(op admissionv1.Operation) context.Context {
		var req admission.Request

		req.Operation = op

		return admission.NewContextWithRequest(context.Background(), req)
	}

	tests := []struct {
		name   string
		d      *TalosClusterDefaulter
		op     admissionv1.Operation
		preset string
		want   string
	}{
		{"setting fills an unset field", &TalosClusterDefaulter{Settings: reader(false, inst)}, admissionv1.Create, "", "autoCorrect"},
		{"a chosen value is kept", &TalosClusterDefaulter{Settings: reader(false, inst)}, admissionv1.Create, settings.DriftPolicyDefault, settings.DriftPolicyDefault},
		{"update is never touched", &TalosClusterDefaulter{Settings: reader(false, inst)}, admissionv1.Update, "", ""},
		{"no setting means warnOnly", &TalosClusterDefaulter{Settings: reader(false)}, admissionv1.Create, "", settings.DriftPolicyDefault},
		{"no reader means warnOnly", &TalosClusterDefaulter{}, admissionv1.Create, "", settings.DriftPolicyDefault},
		{"read-only ignores the spec", &TalosClusterDefaulter{Settings: reader(true, inst)}, admissionv1.Create, "", settings.DriftPolicyDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{DriftPolicy: tt.preset}}
			if err := tt.d.Default(ctxOf(tt.op), c); err != nil {
				t.Fatal(err)
			}

			if c.Spec.DriftPolicy != tt.want {
				t.Fatalf("driftPolicy = %q, want %q", c.Spec.DriftPolicy, tt.want)
			}
		})
	}
}
