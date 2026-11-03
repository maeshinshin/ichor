// Package settings resolves Ichor's runtime settings (ADR 0105, Part B): a field that is set in
// the IchorInstallation spec wins, an unset one falls back to the flag or chart value, and
// with the read-only switch every spec field is ignored.
package settings

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// SingletonName is the name of the one IchorInstallation.
const SingletonName = "ichor"

// DriftPolicyDefault is what a new TalosCluster gets when the setting is unset.
const DriftPolicyDefault = DriftWarnOnly

// Fallback holds the flag or chart values used for an unset field.
type Fallback struct {
	TokenTTL        time.Duration
	RefreshLifetime time.Duration
	// DriftPolicy empty means DriftPolicyDefault.
	DriftPolicy string
	// UpdateCheck is the --update-check flag: whether the controller looks for newer releases (ADR 0114).
	UpdateCheck bool
}

// Value is a setting and the place it came from (ichorv1alpha1.IchorSourceSpec or IchorSourceDeployment).
type Value[T any] struct {
	V      T
	Source string
}

// Effective is every runtime setting in force.
type Effective struct {
	TokenTTL        Value[time.Duration]
	RefreshLifetime Value[time.Duration]
	DriftPolicy     Value[string]
	UpdateCheck     Value[bool]
}

func pick[T any](spec *T, fb T, readOnly bool) Value[T] {
	if spec != nil && !readOnly {
		return Value[T]{*spec, ichorv1alpha1.IchorSourceSpec}
	}

	return Value[T]{fb, ichorv1alpha1.IchorSourceDeployment}
}

func durationOf(d *metav1.Duration) *time.Duration {
	if d == nil {
		return nil
	}

	return &d.Duration
}

// Resolve applies the precedence rule. inst may be nil (no IchorInstallation).
func Resolve(inst *ichorv1alpha1.IchorInstallation, fb Fallback, readOnly bool) Effective {
	var (
		ttl, refr   *time.Duration
		driftPolicy *string
		updateCheck *bool
	)

	if inst != nil {
		if u := inst.Spec.Users; u != nil {
			ttl, refr = durationOf(u.TokenTTL), durationOf(u.RefreshLifetime)
		}

		if d := inst.Spec.Defaults; d != nil && d.DriftPolicy != "" {
			driftPolicy = &d.DriftPolicy
		}

		if c := inst.Spec.UpdateCheck; c != nil {
			updateCheck = c.Enabled
		}
	}

	if fb.DriftPolicy == "" {
		fb.DriftPolicy = DriftPolicyDefault
	}

	return Effective{
		TokenTTL:        pick(ttl, fb.TokenTTL, readOnly),
		RefreshLifetime: pick(refr, fb.RefreshLifetime, readOnly),
		DriftPolicy:     pick(driftPolicy, fb.DriftPolicy, readOnly),
		UpdateCheck:     pick(updateCheck, fb.UpdateCheck, readOnly),
	}
}

// Reader reads the singleton at call time and resolves it. A short TTL keeps a busy caller to
// one GET per interval; zero reads every time.
type Reader struct {
	Client   client.Reader
	Fallback Fallback
	ReadOnly bool
	TTL      time.Duration

	mu   sync.Mutex
	at   time.Time
	last Effective
	ok   bool
}

// Effective returns the settings in force. A missing IchorInstallation gives the fallback. A read
// error gives the last good answer, or the fallback with the error when there is none.
func (r *Reader) Effective(ctx context.Context) (Effective, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ok && r.TTL > 0 && time.Since(r.at) < r.TTL {
		return r.last, nil
	}

	var inst ichorv1alpha1.IchorInstallation

	err := r.Client.Get(ctx, types.NamespacedName{Name: SingletonName}, &inst)

	switch {
	case err == nil:
		r.last = Resolve(&inst, r.Fallback, r.ReadOnly)
	case apierrors.IsNotFound(err):
		r.last = Resolve(nil, r.Fallback, r.ReadOnly)
	case r.ok:
		return r.last, nil
	default:
		return Resolve(nil, r.Fallback, r.ReadOnly), err
	}

	r.ok, r.at = true, time.Now()

	return r.last, nil
}
