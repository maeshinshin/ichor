package settings

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// Changes are the settings to change; nil means unchanged. shortcut: no way to unset a field
// again (back to the deployment value) yet.
type Changes struct {
	TokenTTL        *time.Duration
	RefreshLifetime *time.Duration
	// DriftPolicy is warnOnly or autoCorrect.
	DriftPolicy *string
	// UpdateCheck turns the check for newer releases on or off.
	UpdateCheck *bool
	// The automatic patch settings; a nil field stays as it is. An empty, non-nil
	// AutoPatchComponents means all three components.
	AutoPatchEnabled    *bool
	AutoPatchComponents []string
	AutoPatchWindow     *string
	// AutoPatchAcknowledge clears a halt: the halted operation's namespace/name.
	AutoPatchAcknowledge *string
}

// Empty reports that nothing is to change.
func (c Changes) Empty() bool {
	return c.TokenTTL == nil && c.RefreshLifetime == nil && c.DriftPolicy == nil && c.UpdateCheck == nil &&
		c.AutoPatchEnabled == nil && c.AutoPatchComponents == nil && c.AutoPatchWindow == nil && c.AutoPatchAcknowledge == nil
}

// OnlyAcknowledge reports that the one change is clearing an automatic patch halt, which a read-only deployment
// still allows: it is an action on a state Ichor wrote, not one of the deployment's settings.
func (c Changes) OnlyAcknowledge() bool {
	only := c
	only.AutoPatchAcknowledge = nil

	return c.AutoPatchAcknowledge != nil && only.Empty()
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

	for _, comp := range c.AutoPatchComponents {
		if !slices.Contains(AllAutoPatchComponents, comp) {
			errs = append(errs, fmt.Errorf("autoPatch.components: %q is not one of %s", comp, strings.Join(AllAutoPatchComponents, ", ")))
		}
	}

	if w := c.AutoPatchWindow; w != nil && *w != "" {
		if _, _, err := ParseWindow(*w); err != nil {
			errs = append(errs, fmt.Errorf("autoPatch.window: %w", err))
		}
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

	if c.AutoPatchEnabled != nil || c.AutoPatchComponents != nil || c.AutoPatchWindow != nil || c.AutoPatchAcknowledge != nil {
		if s.AutoPatch == nil {
			s.AutoPatch = &ichorv1alpha1.IchorAutoPatchSettings{}
		}

		if c.AutoPatchEnabled != nil {
			s.AutoPatch.Enabled = c.AutoPatchEnabled
		}

		if c.AutoPatchComponents != nil {
			s.AutoPatch.Components = c.AutoPatchComponents
		}

		if c.AutoPatchWindow != nil {
			s.AutoPatch.Window = *c.AutoPatchWindow
		}

		if c.AutoPatchAcknowledge != nil {
			s.AutoPatch.AcknowledgedHalt = *c.AutoPatchAcknowledge
		}
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
	// The automatic patch keys. autoPatch.components is a comma-separated list (empty: all);
	// autoPatch.window is "HH:MM-HH:MM" UTC (empty: any time); autoPatch.acknowledgeHalt is the halted
	// operation's namespace/name.
	KeyAutoPatchEnabled    = "autoPatch.enabled"
	KeyAutoPatchComponents = "autoPatch.components"
	KeyAutoPatchWindow     = "autoPatch.window"
	KeyAutoPatchAckHalt    = "autoPatch.acknowledgeHalt"
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
		case KeyUpdateCheck, KeyAutoPatchEnabled:
			var b bool

			if b, err = strconv.ParseBool(v); err == nil {
				if k == KeyUpdateCheck {
					c.UpdateCheck = &b
				} else {
					c.AutoPatchEnabled = &b
				}
			}
		case KeyAutoPatchComponents:
			c.AutoPatchComponents = []string{}

			for comp := range strings.SplitSeq(v, ",") {
				if comp = strings.TrimSpace(comp); comp != "" {
					c.AutoPatchComponents = append(c.AutoPatchComponents, comp)
				}
			}
		case KeyAutoPatchWindow:
			c.AutoPatchWindow = &v
		case KeyAutoPatchAckHalt:
			c.AutoPatchAcknowledge = &v
		default:
			return c, fmt.Errorf("unknown setting %q (known: %s, %s, %s, %s, %s, %s, %s, %s)", k, KeyTokenTTL, KeyRefreshLifetime, KeyDriftPolicy, KeyUpdateCheck,
				KeyAutoPatchEnabled, KeyAutoPatchComponents, KeyAutoPatchWindow, KeyAutoPatchAckHalt)
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

// ParseWindow reads a daily UTC window "HH:MM-HH:MM" as minutes since midnight. The end may be before the
// start (the window wraps midnight); equal ends are refused as an empty window.
func ParseWindow(w string) (from, to int, err error) {
	a, b, ok := strings.Cut(w, "-")
	if !ok {
		return 0, 0, fmt.Errorf("%q: want HH:MM-HH:MM", w)
	}

	from, err = parseClock(a)
	if err != nil {
		return 0, 0, fmt.Errorf("%q: %w", w, err)
	}

	to, err = parseClock(b)
	if err != nil {
		return 0, 0, fmt.Errorf("%q: %w", w, err)
	}

	if from == to {
		return 0, 0, fmt.Errorf("%q: the window is empty", w)
	}

	return from, to, nil
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}

	return t.Hour()*60 + t.Minute(), nil
}

// InWindow reports whether now (UTC) is inside the window; an empty window is always open. An
// unparsable window is never open.
func InWindow(w string, now time.Time) bool {
	if w == "" {
		return true
	}

	from, to, err := ParseWindow(w)
	if err != nil {
		return false
	}

	now = now.UTC()
	m := now.Hour()*60 + now.Minute()

	if from < to {
		return m >= from && m < to
	}

	return m >= from || m < to
}
