/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package secureboot holds the pure rules: which installer image a node gets, what
// kernel lockdown level a command line means, and how a node's reported boot security compares
// with spec.security.secureBoot and spec.security.kernelLockdown. The controller, the admission
// webhook and the API server share it so they cannot disagree.
package secureboot

import (
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// Image Factory's installer reference shape (Talos v1.14, "Boot assets" and "SecureBoot"):
// factory.talos.dev/metal-installer/<schematic>:<version>, and metal-installer-secureboot for the
// signed UKI variant.
const (
	imageFactoryHost = "factory.talos.dev"
	installer        = "metal-installer"
	installerSecure  = "metal-installer-secureboot"
)

// InstallerImage is the Image Factory installer reference for schematic and Talos version.
func InstallerImage(schematic, version string, secureBoot bool) string {
	name := installer
	if secureBoot {
		name = installerSecure
	}

	return fmt.Sprintf("%s/%s/%s:%s", imageFactoryHost, name, schematic, version)
}

// ClusterInstallerImage is InstallerImage for cluster's schematic (spec.schematic, else the stock
// installer's) and spec.talosVersion.
func ClusterInstallerImage(cluster *ichorv1alpha1.TalosCluster, secureBoot bool) string {
	schematic := upgrade.DefaultSchematic
	if cluster.Spec.Schematic != nil && cluster.Spec.Schematic.ID != "" {
		schematic = cluster.Spec.Schematic.ID
	}

	return InstallerImage(schematic, cluster.Spec.TalosVersion, secureBoot)
}

// IsSecureBootInstaller reports whether image names a SecureBoot installer (the stock Image
// Factory one or a mirror that keeps the path, "installer-secureboot").
func IsSecureBootInstaller(image string) bool {
	return strings.Contains(image, "installer-secureboot")
}

// Mode is the SecureBoot mode of sec; unset is Off.
func Mode(sec *ichorv1alpha1.TalosClusterSecurity) string {
	if sec == nil || sec.SecureBoot == nil || sec.SecureBoot.Mode == "" {
		return ichorv1alpha1.SecureBootOff
	}

	return sec.SecureBoot.Mode
}

// UseSecureBootInstaller decides the installer variant for a Talos upgrade of one node. A node
// that runs SecureBoot always gets the signed one (an unsigned UKI would not boot there). Otherwise
// the signed one is used when the mode is not Off and the node booted a UKI; a GRUB install, or a
// node not read yet, keeps the stock installer because Talos cannot convert it.
func UseSecureBootInstaller(sec *ichorv1alpha1.TalosClusterSecurity, node *ichorv1alpha1.TalosMachineSecurity) bool {
	if node == nil {
		return false
	}

	if node.SecureBoot {
		return true
	}

	return Mode(sec) != ichorv1alpha1.SecureBootOff && node.BootedWithUKI
}

// lockdownRank orders the levels; an unknown level ranks below none.
var lockdownRank = map[string]int{
	ichorv1alpha1.KernelLockdownNone:            0,
	ichorv1alpha1.KernelLockdownIntegrity:       1,
	ichorv1alpha1.KernelLockdownConfidentiality: 2,
}

// LockdownLevel is the kernel lockdown level a command line and the SecureBoot state mean: the
// last lockdown= word with a known value, else integrity under SecureBoot (Talos v1.14 runs
// SecureBoot images at integrity without an argument), else none.
func LockdownLevel(cmdline string, secureBoot bool) string {
	level := ""

	for w := range strings.FieldsSeq(cmdline) {
		if v, ok := strings.CutPrefix(w, "lockdown="); ok {
			if _, known := lockdownRank[v]; known {
				level = v
			}
		}
	}

	switch {
	case level != "":
		return level
	case secureBoot:
		return ichorv1alpha1.KernelLockdownIntegrity
	default:
		return ichorv1alpha1.KernelLockdownNone
	}
}

// LockdownMeets reports whether observed is at least expected. An unknown or empty observed level
// meets nothing.
func LockdownMeets(observed, expected string) bool {
	o, ok := lockdownRank[observed]

	return ok && o >= lockdownRank[expected]
}

// MachineConditions are the conditions one machine carries for the options set in sec, from what
// the node reported (nil when it was not read yet). An option that is not set contributes no
// condition, so a cluster that does not use them shows none. The messages are stable for a given
// state, so an unchanged state writes nothing.
func MachineConditions(
	sec *ichorv1alpha1.TalosClusterSecurity, node *ichorv1alpha1.TalosMachineSecurity,
) []metav1.Condition {
	var out []metav1.Condition

	if sec == nil {
		return nil
	}

	// Off reports nothing, except a node that runs SecureBoot anyway.
	if m := Mode(sec); m != ichorv1alpha1.SecureBootOff || sec.SecureBoot != nil && node != nil && node.SecureBoot {
		out = append(out, secureBootCondition(m, node))
	}

	if sec.KernelLockdown != "" {
		out = append(out, lockdownCondition(sec.KernelLockdown, node))
	}

	return out
}

// reasonNotRead is the reason of an Unknown condition whose node has not been read yet.
const reasonNotRead = "NotRead"

func condition(typ string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{Type: typ, Status: status, Reason: reason, Message: message}
}

func secureBootCondition(mode string, node *ichorv1alpha1.TalosMachineSecurity) metav1.Condition {
	const typ = ichorv1alpha1.ConditionSecureBoot

	switch {
	case node == nil:
		return condition(typ, metav1.ConditionUnknown, reasonNotRead, "the node's security state has not been read yet")
	case mode == ichorv1alpha1.SecureBootOff:
		return condition(typ, metav1.ConditionFalse, "ModeOffButSecureBoot",
			"spec.security.secureBoot.mode is Off but the node runs SecureBoot: upgrades keep using the SecureBoot "+
				"installer (the stock one would not boot); set the mode to Preferred or Required")
	case node.SecureBoot:
		return condition(typ, metav1.ConditionTrue, "SecureBootOn", "the node booted with SecureBoot enabled")
	case !node.BootedWithUKI:
		return condition(typ, metav1.ConditionFalse, "NotUKI",
			"the node booted from GRUB, not a Unified Kernel Image: Talos cannot convert it, so SecureBoot needs the "+
				"node to be reset and added again from the SecureBoot ISO (docs/guides/hardening.md)")
	default:
		return condition(typ, metav1.ConditionFalse, "SecureBootOff",
			"the node booted a UKI with SecureBoot disabled in the firmware: enable SecureBoot and enrol the keys (the "+
				"firmware in setup mode), or reset and add the node from the SecureBoot ISO (docs/guides/hardening.md)")
	}
}

func lockdownCondition(expected string, node *ichorv1alpha1.TalosMachineSecurity) metav1.Condition {
	const typ = ichorv1alpha1.ConditionKernelLockdown

	switch {
	case node == nil || node.KernelLockdown == "":
		return condition(typ, metav1.ConditionUnknown, reasonNotRead, "the node's lockdown level has not been read yet")
	case LockdownMeets(node.KernelLockdown, expected):
		return condition(typ, metav1.ConditionTrue, "LockdownMet",
			fmt.Sprintf("the kernel runs with lockdown %s (expected at least %s)", node.KernelLockdown, expected))
	default:
		return condition(typ, metav1.ConditionFalse, "LockdownBelowExpected",
			fmt.Sprintf("the kernel runs with lockdown %s, expected at least %s: the kernel command line is part of the "+
				"boot asset; build lockdown=%s into it with an Image Factory schematic (spec.schematic) and reinstall "+
				"(docs/guides/hardening.md)", node.KernelLockdown, expected, expected))
	}
}

// ClusterCondition summarises one condition type over the approved machines (name to conditions):
// False when any machine's is False (naming them), else Unknown when any is Unknown or missing,
// else True.
func ClusterCondition(typ string, machines map[string][]metav1.Condition) metav1.Condition {
	c := metav1.Condition{Type: typ}

	var bad []string

	unknown := 0

	for name, conds := range machines {
		var found *metav1.Condition

		for i := range conds {
			if conds[i].Type == typ {
				found = &conds[i]
			}
		}

		switch {
		case found == nil || found.Status == metav1.ConditionUnknown:
			unknown++
		case found.Status == metav1.ConditionFalse:
			bad = append(bad, fmt.Sprintf("%s (%s)", name, found.Reason))
		}
	}

	slices.Sort(bad)

	switch {
	case len(machines) == 0:
		c.Status, c.Reason, c.Message = metav1.ConditionUnknown, "NoMachines", "the cluster has no approved nodes yet"
	case len(bad) > 0:
		c.Status, c.Reason = metav1.ConditionFalse, "NodesNotConformant"
		c.Message = fmt.Sprintf("%d of %d nodes do not match: %s; the node's %s condition says what to do",
			len(bad), len(machines), strings.Join(bad, ", "), typ)
	case unknown > 0:
		c.Status, c.Reason = metav1.ConditionUnknown, reasonNotRead
		c.Message = fmt.Sprintf("%d of %d nodes have not been read yet", unknown, len(machines))
	default:
		c.Status, c.Reason = metav1.ConditionTrue, "AllNodesConformant"
		c.Message = fmt.Sprintf("all %d nodes match", len(machines))
	}

	return c
}
