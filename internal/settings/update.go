package settings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// Changes are the settings to change; nil means unchanged. ponytail: no way to unset a field
// again (back to the deployment value) yet.
type Changes struct {
	TokenTTL        *time.Duration
	RefreshLifetime *time.Duration
	// DriftPolicy is warnOnly or autoCorrect.
	DriftPolicy *string
	// UpdateCheck turns the check for newer releases on or off.
	UpdateCheck *bool
}

// Empty reports that nothing is to change.
func (c Changes) Empty() bool {
	return c.TokenTTL == nil && c.RefreshLifetime == nil && c.DriftPolicy == nil && c.UpdateCheck == nil
}

// Validate checks the bounds the CRD enforces, so the caller gets the message before the API server's.
func (c Changes) Validate() error {
	var errs []error

	if t := c.TokenTTL; t != nil && (*t < ichorauth.MinTokenTTL || *t > ichorauth.MaxTokenTTL) {
		errs = append(errs, fmt.Errorf("users.tokenTTL must be between %s and %s", ichorauth.MinTokenTTL, ichorauth.MaxTokenTTL))
	}

	if r := c.RefreshLifetime; r != nil && (*r < ichorauth.MinRefreshLifetime || *r > ichorauth.MaxRefreshLifetime) {
		errs = append(errs, fmt.Errorf("users.refreshLifetime must be between %s and %s", ichorauth.MinRefreshLifetime, ichorauth.MaxRefreshLifetime))
	}

	if d := c.DriftPolicy; d != nil && *d != DriftWarnOnly && *d != DriftAutoCorrect {
		errs = append(errs, fmt.Errorf("defaults.driftPolicy must be warnOnly or autoCorrect, not %q", *d))
	}

	return errors.Join(errs...)
}

// Apply writes the changes into inst's spec. Only the changed fields are touched. The caller validates.
func (c Changes) Apply(inst *ichorv1alpha1.IchorInstallation) {
	s := &inst.Spec

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

	if c.UpdateCheck != nil {
		s.UpdateCheck = &ichorv1alpha1.IchorUpdateCheckSettings{Enabled: c.UpdateCheck}
	}
}

// Keys of ParseAssignments, as `ichor settings set` spells them.
const (
	// KeyReclaim is the removed switch: ParseAssignments refuses it.
	KeyReclaim         = "reclaim.enabled"
	KeyTokenTTL        = "users.tokenTTL"
	KeyRefreshLifetime = "users.refreshLifetime"
	KeyDriftPolicy     = "defaults.driftPolicy"
	KeyUpdateCheck     = "updateCheck.enabled"
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
			return c, errors.New("reclaim.enabled is removed: who may reset and add a machine is decided by RBAC alone (the reclaim verb on machinereclaims)")
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
		case KeyUpdateCheck:
			var b bool

			if b, err = strconv.ParseBool(v); err == nil {
				c.UpdateCheck = &b
			}
		default:
			return c, fmt.Errorf("unknown setting %q (known: %s, %s, %s, %s)", k, KeyTokenTTL, KeyRefreshLifetime, KeyDriftPolicy, KeyUpdateCheck)
		}

		if err != nil {
			return c, fmt.Errorf("%s: %w", k, err)
		}
	}

	return c, c.Validate()
}

// The values of spec.defaults.driftPolicy.
const (
	DriftWarnOnly    = "warnOnly"
	DriftAutoCorrect = "autoCorrect"
)
