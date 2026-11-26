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

// A TalosStorage being deleted is still read by the controller: an invalid selector set while it is deleting
// is refused, a finalizer-only change is admitted.
func TestTalosStorageValidatesASpecChangeWhileDeleting(t *testing.T) {
	t.Parallel()

	v := &TalosStorageCustomValidator{}
	now := metav1.Now()

	deleting := func(sel metav1.LabelSelector) *ichorv1alpha1.TalosStorage {
		s := &ichorv1alpha1.TalosStorage{Spec: ichorv1alpha1.TalosStorageSpec{Nodes: sel}}
		s.DeletionTimestamp, s.Finalizers = &now, []string{"example.com/foreign"}

		return s
	}

	for name, sel := range badSelectors() {
		if _, err := v.ValidateUpdate(context.Background(), deleting(metav1.LabelSelector{}), deleting(*sel)); err == nil {
			t.Errorf("%s: a deleting TalosStorage was admitted with an invalid spec.nodes", name)
		}
	}

	same := deleting(*badSelectors()["operator Equals"])
	noFinalizer := same.DeepCopy()
	noFinalizer.Finalizers = nil

	if _, err := v.ValidateUpdate(context.Background(), same, noFinalizer); err != nil {
		t.Errorf("a finalizer-only change while deleting was refused: %v", err)
	}
}
