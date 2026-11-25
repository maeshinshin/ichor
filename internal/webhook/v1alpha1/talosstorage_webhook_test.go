package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// badSelectors are admitted by the CRD (operator is a plain string) and fail metav1.LabelSelectorAsSelector.
func badSelectors() map[string]*metav1.LabelSelector {
	req := func(op metav1.LabelSelectorOperator, vals ...string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "k", Operator: op, Values: vals}}}
	}

	return map[string]*metav1.LabelSelector{
		"operator Equals":   req("Equals", "v"),
		"In with no values": req(metav1.LabelSelectorOpIn),
	}
}

func TestTalosStorageRefusesAnInvalidSelector(t *testing.T) {
	t.Parallel()

	v := &TalosStorageCustomValidator{}

	for name, sel := range badSelectors() {
		s := &ichorv1alpha1.TalosStorage{Spec: ichorv1alpha1.TalosStorageSpec{Nodes: *sel}}

		if _, err := v.ValidateCreate(context.Background(), s); err == nil {
			t.Errorf("%s: create admitted", name)
		}

		if _, err := v.ValidateUpdate(context.Background(), &ichorv1alpha1.TalosStorage{}, s); err == nil {
			t.Errorf("%s: update admitted", name)
		}
	}

	ok := &ichorv1alpha1.TalosStorage{Spec: ichorv1alpha1.TalosStorageSpec{Nodes: metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}}}
	if _, err := v.ValidateCreate(context.Background(), ok); err != nil {
		t.Errorf("a valid selector is refused: %v", err)
	}
}
