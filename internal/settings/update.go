package settings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Bounds from the CRD's CEL rules.
const (
	MinTokenTTL        = time.Minute
	MaxTokenTTL        = time.Hour
	MinRefreshLifetime = time.Hour
	MaxRefreshLifetime = 720 * time.Hour
)

// Changes are the settings to change; nil means unchanged. ponytail: no way to unset a field
// again (back to the deployment value) yet.
type Changes struct {
	Reclaim         *bool
	TokenTTL        *time.Duration
	RefreshLifetime *time.Duration
	// DriftPolicy is warnOnly or autoCorrect.
	DriftPolicy *string
}

// Empty reports that nothing is to change.
func (c Changes) Empty() bool {
	return c.Reclaim == nil && c.TokenTTL == nil && c.RefreshLifetime == nil && c.DriftPolicy == nil
}

// Validate checks the bounds the CRD enforces, so the caller gets the message before the API server's.
func (c Changes) Validate() error {
	var errs []error

	if t := c.TokenTTL; t != nil && (*t < MinTokenTTL || *t > MaxTokenTTL) {
		errs = append(errs, fmt.Errorf("users.tokenTTL must be between %s and %s", MinTokenTTL, MaxTokenTTL))
	}

	if r := c.RefreshLifetime; r != nil && (*r < MinRefreshLifetime || *r > MaxRefreshLifetime) {
		errs = append(errs, fmt.Errorf("users.refreshLifetime must be between %s and %s", MinRefreshLifetime, MaxRefreshLifetime))
	}

	if d := c.DriftPolicy; d != nil && *d != "warnOnly" && *d != "autoCorrect" {
		errs = append(errs, fmt.Errorf("defaults.driftPolicy must be warnOnly or autoCorrect, not %q", *d))
	}

	return errors.Join(errs...)
}

// NeedsConfirmation reports that the change enables reclaim.
func (c Changes) NeedsConfirmation() bool { return c.Reclaim != nil && *c.Reclaim }

// Apply writes the changes into inst's spec and sets the confirm annotation when it enables
// reclaim (and drops a stale one otherwise). Only the changed fields are touched.
func (c Changes) Apply(inst *ichorv1alpha1.IchorInstallation) error {
	if err := c.Validate(); err != nil {
		return err
	}

	s := &inst.Spec

	if c.Reclaim != nil {
		s.Reclaim = &ichorv1alpha1.IchorReclaimSettings{Enabled: c.Reclaim}
	}

	if c.TokenTTL != nil || c.RefreshLifetime != nil {
		if s.Users == nil {
			s.Users = &ichorv1alpha1.IchorUserSettings{}
		}

		if c.TokenTTL != nil {
			s.Users.TokenTTL = &metav1.Duration{Duration: *c.TokenTTL}
		}

		if c.RefreshLifetime != nil {
			s.Users.RefreshLifetime = &metav1.Duration{Duration: *c.RefreshLifetime}
		}
	}

	if c.DriftPolicy != nil {
		s.Defaults = &ichorv1alpha1.IchorDefaultSettings{DriftPolicy: *c.DriftPolicy}
	}

	if c.NeedsConfirmation() {
		if inst.Annotations == nil {
			inst.Annotations = map[string]string{}
		}

		inst.Annotations[ichorv1alpha1.ConfirmReclaimAnnotation] = ichorv1alpha1.ConfirmReclaimValue
	} else {
		delete(inst.Annotations, ichorv1alpha1.ConfirmReclaimAnnotation)
	}

	return nil
}

// Keys of ParseAssignments, as `ichor settings set` spells them.
const (
	KeyReclaim         = "reclaim.enabled"
	KeyTokenTTL        = "users.tokenTTL"
	KeyRefreshLifetime = "users.refreshLifetime"
	KeyDriftPolicy     = "defaults.driftPolicy"
)

// ParseAssignments turns KEY=VALUE arguments into Changes.
func ParseAssignments(args []string) (Changes, error) {
	var c Changes

	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return c, fmt.Errorf("%q: want KEY=VALUE", a)
		}

		var err error

		switch k {
		case KeyReclaim:
			var b bool

			if b, err = strconv.ParseBool(v); err == nil {
				c.Reclaim = &b
			}
		case KeyTokenTTL, KeyRefreshLifetime:
			var d time.Duration

			if d, err = time.ParseDuration(v); err == nil {
				if k == KeyTokenTTL {
					c.TokenTTL = &d
				} else {
					c.RefreshLifetime = &d
				}
			}
		case KeyDriftPolicy:
			c.DriftPolicy = &v
		default:
			return c, fmt.Errorf("unknown setting %q (known: %s, %s, %s, %s)", k, KeyReclaim, KeyTokenTTL, KeyRefreshLifetime, KeyDriftPolicy)
		}

		if err != nil {
			return c, fmt.Errorf("%s: %w", k, err)
		}
	}

	return c, c.Validate()
}
