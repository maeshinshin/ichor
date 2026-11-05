package v1alpha1

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// SettingsGate guards the fields of IchorInstallation.spec that are Ichor's settings (ADR 0105,
// decision 9): the finer RBAC split by SubjectAccessReview on ichorinstallations/{version,settings},
// --settings-read-only, and the Event that names who changed what. spec.reclaim is ignored (ADR 0107).
type SettingsGate struct {
	// Client is uncached: it creates SubjectAccessReviews.
	Client client.Client
	// ReadOnly denies every write to spec.users and spec.defaults (--settings-read-only).
	ReadOnly bool
	// ExemptUsers (the controller) are never stopped.
	ExemptUsers []string
	// Recorder emits the change Event; nil emits none.
	Recorder record.EventRecorder
}

const pathAutoPatchAck = "spec.autoPatch.acknowledgedHalt"

// settingChange is one changed field of the spec.
type settingChange struct {
	path, old, new string
	// subresource is the RBAC subresource the change needs.
	subresource string
}

// boolOf is the update check switch as "true", "false" or "" when unset.
func boolOf(c *ichorv1alpha1.IchorUpdateCheckSettings) string {
	if c == nil || c.Enabled == nil {
		return ""
	}

	return strconv.FormatBool(*c.Enabled)
}

// autoPatchOf is the automatic patch settings as an old and a new value per field; empty when unset.
func autoPatchOf(a *ichorv1alpha1.IchorAutoPatchSettings) (enabled, components, window, ack string) {
	if a == nil {
		return "", "", "", ""
	}

	if a.Enabled != nil {
		enabled = strconv.FormatBool(*a.Enabled)
	}

	return enabled, strings.Join(a.Components, ","), a.Window, a.AcknowledgedHalt
}

func durOf(d *metav1.Duration) string {
	if d == nil {
		return ""
	}

	return d.Duration.String()
}

// settingChanges lists the changed fields of the spec, in a fixed order. old may be nil (a create).
func settingChanges(oldSpec, newSpec *ichorv1alpha1.IchorInstallationSpec) []settingChange {
	oldSpec = cmp.Or(oldSpec, &ichorv1alpha1.IchorInstallationSpec{})

	var (
		oU, nU     = cmp.Or(oldSpec.Users, &ichorv1alpha1.IchorUserSettings{}), cmp.Or(newSpec.Users, &ichorv1alpha1.IchorUserSettings{})
		oD, nD     = cmp.Or(oldSpec.Defaults, &ichorv1alpha1.IchorDefaultSettings{}), cmp.Or(newSpec.Defaults, &ichorv1alpha1.IchorDefaultSettings{})
		oDep, nDep = cmp.Or(oldSpec.Deployment, &ichorv1alpha1.IchorDeploymentSpec{}), cmp.Or(newSpec.Deployment, &ichorv1alpha1.IchorDeploymentSpec{})
		oSig, nSig = cmp.Or(oldSpec.InstallerSignature, &ichorv1alpha1.IchorInstallerSignature{}), cmp.Or(newSpec.InstallerSignature, &ichorv1alpha1.IchorInstallerSignature{})
	)

	oAE, oAC, oAW, oAK := autoPatchOf(oldSpec.AutoPatch)
	nAE, nAC, nAW, nAK := autoPatchOf(newSpec.AutoPatch)

	const (
		ver = ichorv1alpha1.IchorSubresourceVersion
		set = ichorv1alpha1.IchorSubresourceSettings
	)

	all := []settingChange{
		{"spec.version", oldSpec.Version, newSpec.Version, ver},
		{"spec.installerRepository", oldSpec.InstallerRepository, newSpec.InstallerRepository, ver},
		{"spec.installerDigest", oldSpec.InstallerDigest, newSpec.InstallerDigest, ver},
		{"spec.installerSignature.mode", oSig.Mode, nSig.Mode, ver},
		{"spec.installerSignature.reason", oSig.Reason, nSig.Reason, ver},
		{"spec.deployment.manager", oDep.Manager, nDep.Manager, ver},
		{"spec.users.tokenTTL", durOf(oU.TokenTTL), durOf(nU.TokenTTL), set},
		{"spec.users.refreshLifetime", durOf(oU.RefreshLifetime), durOf(nU.RefreshLifetime), set},
		{"spec.defaults.driftPolicy", oD.DriftPolicy, nD.DriftPolicy, set},
		{"spec.updateCheck.enabled", boolOf(oldSpec.UpdateCheck), boolOf(newSpec.UpdateCheck), set},
		{"spec.autoPatch.enabled", oAE, nAE, set},
		{"spec.autoPatch.components", oAC, nAC, set},
		{"spec.autoPatch.window", oAW, nAW, set},
		{pathAutoPatchAck, oAK, nAK, set},
	}

	return slices.DeleteFunc(all, func(c settingChange) bool { return c.old == c.new })
}

// check applies the gate to a write; a nil gate is off. oldObj is nil on create.
func (g *SettingsGate) check(ctx context.Context, oldObj, newObj *ichorv1alpha1.IchorInstallation) error {
	if g == nil {
		return nil
	}

	var oldSpec *ichorv1alpha1.IchorInstallationSpec
	if oldObj != nil {
		oldSpec = &oldObj.Spec
	}

	changes := settingChanges(oldSpec, &newObj.Spec)
	if len(changes) == 0 {
		return nil
	}

	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return fmt.Errorf("the admission request is missing, cannot tell who writes: %w", err)
	}

	if slices.Contains(g.ExemptUsers, req.UserInfo.Username) {
		return nil
	}

	if g.ReadOnly {
		for _, c := range changes {
			// Clearing an automatic patch halt is an action, not a setting: --settings-read-only does not stop it (ADR 0116).
			if c.subresource != ichorv1alpha1.IchorSubresourceVersion && c.path != pathAutoPatchAck {
				return fmt.Errorf("%s: settings are managed by the deployment (--settings-read-only / chart settings.readOnly); change them there", c.path)
			}
		}
	}

	bg := &OverrideHandler{Client: g.Client}

	var checked []string

	for _, c := range changes {
		if slices.Contains(checked, c.subresource) {
			continue
		}

		checked = append(checked, c.subresource)

		r := bg.require(ctx, req, "update", "ichorinstallations", c.subresource, newObj.Name,
			fmt.Sprintf("changing %s needs the \"update\" verb on ichorinstallations/%s", c.path, c.subresource))
		if r != nil {
			return errors.New(r.Result.Message)
		}
	}

	if req.DryRun == nil || !*req.DryRun {
		g.emit(newObj, req.UserInfo.Username, changes)
	}

	return nil
}

// emit records one Event listing the changed paths, old and new values and the user. Nothing in
// the listed fields is secret.
func (g *SettingsGate) emit(obj *ichorv1alpha1.IchorInstallation, user string, changes []settingChange) {
	if g.Recorder == nil {
		return
	}

	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		parts = append(parts, fmt.Sprintf("%s: %s -> %s", c.path, cmp.Or(c.old, "<unset>"), cmp.Or(c.new, "<unset>")))
	}

	g.Recorder.Eventf(obj, "Normal", "SettingsChanged", "%s changed %s", user, strings.Join(parts, "; "))
}
