package v1alpha1

import (
	"context"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// +kubebuilder:webhook:path=/mutate-ichor-maesh-dev-v1alpha1-taloscluster,mutating=true,failurePolicy=ignore,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters,verbs=create,versions=v1alpha1,name=mtaloscluster-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosClusterDefaulter fills spec.driftPolicy of a new TalosCluster from the installation's
// spec.defaults.driftPolicy. It acts on create only: an existing cluster is never touched, so
// the controller still never mutates a spec. The CRD carries no default for the field, so an unset value
// here means the user did not choose one; with no setting the default is warnOnly, as before.
// failurePolicy is Ignore: defaulting is best effort and a controller outage must not block a create
// (an empty driftPolicy is treated as warnOnly by the reconciler).
//
// +kubebuilder:object:generate=false
type TalosClusterDefaulter struct {
	Settings *settings.Reader
}

// Default implements admission.Defaulter.
func (d *TalosClusterDefaulter) Default(ctx context.Context, c *ichorv1alpha1.TalosCluster) error {
	if req, err := admission.RequestFromContext(ctx); err != nil || req.Operation != admissionv1.Create {
		return nil
	}

	if c.Spec.DriftPolicy != "" {
		return nil
	}

	policy := settings.DriftPolicyDefault

	if d.Settings != nil {
		// A read error yields the fallback, warnOnly.
		e, _ := d.Settings.Effective(ctx)
		policy = e.DriftPolicy.V
	}

	c.Spec.DriftPolicy = policy

	return nil
}
