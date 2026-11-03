// Package autopatch decides whether the controller starts an automatic patch upgrade. The
// decision is a pure function of what the controller read, so every guard has a table test; the loop
// that reads the cluster and writes the operation is internal/controller's AutoPatcher.
package autopatch

import (
	"fmt"
	"time"

	"golang.org/x/mod/semver"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/pkg/release"
)

// MaxListAge is how old the release check's lists may be: the check runs every 6 hours, so a list older
// than two days means the check is failing or off, and its candidates are not trusted to be current.
const MaxListAge = 48 * time.Hour

// MinGap is the least time between two starts, so a start that the reads have not caught up with yet
// cannot be repeated.
const MinGap = 2 * time.Minute

// Cluster is what the decision needs to know about one TalosCluster.
type Cluster struct {
	// Talos and Kubernetes are spec.talosVersion and spec.kubernetesVersion, with a leading v.
	Talos, Kubernetes string
	// CNIUnmanaged is true for the unmanaged CNI preset: Ichor does not operate Cilium there.
	CNIUnmanaged bool
	// CNIDetected is status.cni.detected ("cilium", another CNI, or "" when not observed yet) and
	// CiliumVersion is the running release without its v ("" when it is not Cilium or not known).
	CNIDetected, CiliumVersion string
	// CiliumChart is the chart version this Ichor embeds, without its v.
	CiliumChart string
	// MachinesReady is true when the cluster has approved machines and every one is Ready (a pending
	// configuration change leaves a machine not Ready, so it counts here).
	MachinesReady bool
}

// Input is everything one decision reads.
type Input struct {
	Now     time.Time
	Setting settings.AutoPatch
	// Enabled is the effective switch; Setting holds the components and the window.
	Enabled bool
	// Halted is true while an earlier automatic operation failed or paused and nobody cleared it.
	Halted bool
	// IchorUpgrading is true while the IchorInstallation's version is not the running one.
	IchorUpgrading bool
	// Targets are the lists of the release check; nil when there is none.
	Targets *ichorv1alpha1.IchorUpgradeTargets
	Cluster Cluster
	// Busy names an unfinished operation of the cluster (running, paused or waiting), or is empty.
	Busy string
	// LockHeld is true while the cluster's operation lock is held.
	LockHeld bool
	// LastStart is when the patcher last started an operation; zero when never.
	LastStart time.Time
}

// Decision is either an operation to start (Component and Target set) or the reason there is none.
type Decision struct {
	Component string
	Target    string
	Skip      string
}

// Start reports whether an operation is to be started.
func (d Decision) Start() bool { return d.Target != "" }

func skip(format string, a ...any) Decision { return Decision{Skip: fmt.Sprintf(format, a...)} }

// Decide applies every guard in order, then picks the newest patch of the first selected component that
// has one, in the order Talos, Cilium, Kubernetes: Talos first because a newer Talos is what the
// Kubernetes table is written against, Cilium before Kubernetes because Kubernetes is the component
// Cilium's tested range limits.
func Decide(in Input) Decision {
	switch {
	case !in.Enabled:
		return skip("automatic patching is off")
	case in.Halted:
		return skip("automatic patching is halted by a failed or paused automatic operation")
	case in.IchorUpgrading:
		return skip("an Ichor upgrade is in progress")
	case !settings.InWindow(in.Setting.Window, in.Now):
		return skip("outside the window %s UTC", in.Setting.Window)
	case in.Targets == nil || in.Targets.CheckedAt == nil:
		return skip("the release check has not listed any releases")
	case in.Now.Sub(in.Targets.CheckedAt.Time) > MaxListAge:
		return skip("the release lists are older than %s", MaxListAge)
	case in.Busy != "":
		return skip("operation %s is not finished", in.Busy)
	case in.LockHeld:
		return skip("the cluster's operation lock is held")
	case !in.Cluster.MachinesReady:
		return skip("not every approved machine is Ready")
	case !in.LastStart.IsZero() && in.Now.Sub(in.LastStart) < MinGap:
		return skip("an operation was started less than %s ago", MinGap)
	}

	for _, comp := range settings.AllAutoPatchComponents {
		if !in.Setting.Has(comp) {
			continue
		}

		if target := Candidate(comp, in.Cluster, in.Targets); target != "" {
			return Decision{Component: comp, Target: target}
		}
	}

	return skip("no patch of a running minor is on offer")
}

// Candidate is the newest patch of the component's running minor that is in the release check's list and
// that the compatibility table accepts for this cluster, or "". It is never a minor, and never a version
// only the override verb would pass: it is taken from the same choices the UI offers.
func Candidate(component string, c Cluster, t *ichorv1alpha1.IchorUpgradeTargets) string {
	var (
		running string
		choices release.Choices
	)

	switch component {
	case ichorv1alpha1.AutoPatchTalos:
		running = c.Talos
		choices = release.TalosChoices(c.Talos, c.Kubernetes, release.Pinned.Talos, t.Talos)
	case ichorv1alpha1.AutoPatchKubernetes:
		// An observed CNI that is not Cilium has no Cilium to match; an unobserved one
		// offers nothing until Cilium is seen, because nothing is known to be compatible with it.
		constrains := c.CNIDetected == "" || c.CNIDetected == "cilium"
		running = c.Kubernetes
		choices = release.KubernetesChoices(c.Kubernetes, c.Talos, c.CiliumVersion, release.Pinned.Kubernetes, constrains, t.Kubernetes)
	case ichorv1alpha1.AutoPatchCilium:
		if c.CNIUnmanaged || c.CNIDetected != "cilium" || c.CiliumVersion == "" {
			return ""
		}

		running = "v" + c.CiliumVersion
		choices = release.CiliumChoices(running, c.CiliumChart, c.Kubernetes, t.Cilium)
	default:
		return ""
	}

	best := ""

	for _, v := range append(append([]string{}, choices.Recommended...), choices.Patch...) {
		if semver.MajorMinor(v) == semver.MajorMinor(running) && semver.Compare(v, running) > 0 &&
			(best == "" || semver.Compare(v, best) > 0) {
			best = v
		}
	}

	return best
}

// Resume reports whether a halt is over: a person acknowledged it (acknowledged equals halted), the halting
// operation completed or was deleted, or a later operation that is not the patcher's completed on the same
// cluster (a person fixed it by hand and moved on). Nothing else clears it, and nothing retries the operation.
func Resume(halted, acknowledged string, op *Halting, later []LaterOperation) bool {
	switch {
	case halted == "":
		return false
	case acknowledged == halted:
		return true
	case op == nil || op.Completed:
		return true
	}

	for _, l := range later {
		if !l.Auto && l.Completed && l.Created.After(op.Created) && l.Cluster == op.Cluster {
			return true
		}
	}

	return false
}

// Halting is the automatic operation a halt names, as far as it still exists.
type Halting struct {
	Cluster   string
	Created   time.Time
	Completed bool
}

// LaterOperation is another operation of the cluster that may end a halt.
type LaterOperation struct {
	Cluster   string
	Created   time.Time
	Auto      bool
	Completed bool
}
