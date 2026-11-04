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

package secureboot_test

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

const (
	schematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"
	reasonOff = "SecureBootOff"
)

func TestInstallerImage(t *testing.T) {
	t.Parallel()

	if got, want := secureboot.InstallerImage(schematic, "v1.14.5", false),
		"factory.talos.dev/metal-installer/"+schematic+":v1.14.5"; got != want {
		t.Errorf("stock = %q, want %q", got, want)
	}

	if got, want := secureboot.InstallerImage(schematic, "v1.14.5", true),
		"factory.talos.dev/metal-installer-secureboot/"+schematic+":v1.14.5"; got != want {
		t.Errorf("secureboot = %q, want %q", got, want)
	}
}

func TestLockdownLevel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		cmdline    string
		secureBoot bool
		want       string
	}{
		{"nothing, no secureboot", "talos.platform=metal", false, "none"},
		{"secureboot is integrity by itself", "talos.platform=metal", true, "integrity"},
		{"explicit confidentiality", "talos.platform=metal lockdown=confidentiality", true, "confidentiality"},
		{"explicit without secureboot", "lockdown=integrity talos.platform=metal", false, "integrity"},
		{"the last word wins", "lockdown=confidentiality lockdown=integrity", false, "integrity"},
		{"an unknown value is ignored", "lockdown=banana", true, "integrity"},
		{"explicit none", "lockdown=none", false, "none"},
		{"not a prefix match", "xlockdown=confidentiality", false, "none"},
	} {
		if got := secureboot.LockdownLevel(tc.cmdline, tc.secureBoot); got != tc.want {
			t.Errorf("%s: LockdownLevel(%q, %v) = %q, want %q", tc.name, tc.cmdline, tc.secureBoot, got, tc.want)
		}
	}
}

func TestLockdownMeets(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		observed, expected string
		want               bool
	}{
		{"none", "integrity", false},
		{"integrity", "integrity", true},
		{"confidentiality", "integrity", true},
		{"integrity", "confidentiality", false},
		{"confidentiality", "confidentiality", true},
		{"", "integrity", false},
	} {
		if got := secureboot.LockdownMeets(tc.observed, tc.expected); got != tc.want {
			t.Errorf("LockdownMeets(%q, %q) = %v, want %v", tc.observed, tc.expected, got, tc.want)
		}
	}
}

func security(mode, lockdown string) *ichorv1alpha1.TalosClusterSecurity {
	s := &ichorv1alpha1.TalosClusterSecurity{KernelLockdown: lockdown}
	if mode != "" {
		s.SecureBoot = &ichorv1alpha1.TalosClusterSecureBoot{Mode: mode}
	}

	return s
}

func TestMode(t *testing.T) {
	t.Parallel()

	if got := secureboot.Mode(nil); got != ichorv1alpha1.SecureBootOff {
		t.Errorf("nil security = %q", got)
	}

	if got := secureboot.Mode(security("", "")); got != ichorv1alpha1.SecureBootOff {
		t.Errorf("no secureBoot = %q", got)
	}

	if got := secureboot.Mode(security("Required", "")); got != ichorv1alpha1.SecureBootRequired {
		t.Errorf("Required = %q", got)
	}
}

func TestUseSecureBootInstaller(t *testing.T) {
	t.Parallel()

	uki := &ichorv1alpha1.TalosMachineSecurity{BootedWithUKI: true}
	grub := &ichorv1alpha1.TalosMachineSecurity{}
	running := &ichorv1alpha1.TalosMachineSecurity{BootedWithUKI: true, SecureBoot: true}

	for _, tc := range []struct {
		name string
		sec  *ichorv1alpha1.TalosClusterSecurity
		node *ichorv1alpha1.TalosMachineSecurity
		want bool
	}{
		{"off, uki node", security("Off", ""), uki, false},
		{"unset, uki node", nil, uki, false},
		{"preferred, uki node", security("Preferred", ""), uki, true},
		{"required, uki node", security("Required", ""), uki, true},
		{"preferred, grub node keeps the stock installer", security("Preferred", ""), grub, false},
		{"preferred, node not read yet", security("Preferred", ""), nil, false},
		{"off, node that runs secureboot is not left unsigned", security("Off", ""), running, true},
		{"unset, node that runs secureboot", nil, running, true},
	} {
		if got := secureboot.UseSecureBootInstaller(tc.sec, tc.node); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func cond(conds []metav1.Condition, typ string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == typ {
			return &conds[i]
		}
	}

	return nil
}

func TestMachineConditions(t *testing.T) {
	t.Parallel()

	t.Run("nothing set, no conditions", func(t *testing.T) {
		t.Parallel()

		if got := secureboot.MachineConditions(nil, &ichorv1alpha1.TalosMachineSecurity{SecureBoot: true}); len(got) != 0 {
			t.Errorf("got %v", got)
		}

		if got := secureboot.MachineConditions(security("Off", ""), nil); len(got) != 0 {
			t.Errorf("Off, got %v", got)
		}
	})

	t.Run("unread is Unknown", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Preferred", "integrity"), nil)
		if c := cond(got, "SecureBoot"); c == nil || c.Status != metav1.ConditionUnknown {
			t.Errorf("SecureBoot = %+v", c)
		}

		if c := cond(got, "KernelLockdown"); c == nil || c.Status != metav1.ConditionUnknown {
			t.Errorf("KernelLockdown = %+v", c)
		}
	})

	t.Run("secureboot on", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Required", ""), &ichorv1alpha1.TalosMachineSecurity{SecureBoot: true, BootedWithUKI: true})
		if c := cond(got, "SecureBoot"); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "SecureBootOn" {
			t.Errorf("SecureBoot = %+v", c)
		}

		if cond(got, "KernelLockdown") != nil {
			t.Error("KernelLockdown set without the option")
		}
	})

	t.Run("a GRUB install needs a reinstall", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Preferred", ""), &ichorv1alpha1.TalosMachineSecurity{})
		c := cond(got, "SecureBoot")
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "NotUKI" {
			t.Fatalf("SecureBoot = %+v", c)
		}
	})

	t.Run("a UKI node with firmware SecureBoot off", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Required", ""), &ichorv1alpha1.TalosMachineSecurity{BootedWithUKI: true})
		c := cond(got, "SecureBoot")
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != reasonOff {
			t.Fatalf("SecureBoot = %+v", c)
		}
	})

	t.Run("mode Off but the node runs SecureBoot", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Off", "integrity"), &ichorv1alpha1.TalosMachineSecurity{
			SecureBoot: true, BootedWithUKI: true, KernelLockdown: "integrity",
		})
		if c := cond(got, "SecureBoot"); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "ModeOffButSecureBoot" {
			t.Errorf("SecureBoot = %+v", c)
		}

		if c := cond(got, "KernelLockdown"); c == nil || c.Status != metav1.ConditionTrue {
			t.Errorf("KernelLockdown = %+v", c)
		}
	})

	t.Run("lockdown below the expected level", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("", "confidentiality"), &ichorv1alpha1.TalosMachineSecurity{
			SecureBoot: true, KernelLockdown: "integrity",
		})

		c := cond(got, "KernelLockdown")
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "LockdownBelowExpected" {
			t.Fatalf("KernelLockdown = %+v", c)
		}

		if cond(got, "SecureBoot") != nil {
			t.Error("SecureBoot set without the option")
		}
	})
}

func TestClusterCondition(t *testing.T) {
	t.Parallel()

	machines := map[string][]metav1.Condition{
		"cp1": {{Type: "SecureBoot", Status: metav1.ConditionTrue}},
		"w1":  {{Type: "SecureBoot", Status: metav1.ConditionFalse, Reason: reasonOff}},
		"w2":  {{Type: "SecureBoot", Status: metav1.ConditionUnknown}},
	}

	c := secureboot.ClusterCondition("SecureBoot", machines)
	if c.Status != metav1.ConditionFalse || c.Reason != "NodesNotConformant" {
		t.Fatalf("got %+v", c)
	}

	if want := "w1"; !strings.Contains(c.Message, want) {
		t.Errorf("message %q does not name %s", c.Message, want)
	}

	if strings.Contains(c.Message, "cp1") {
		t.Errorf("message %q names a conformant machine", c.Message)
	}

	delete(machines, "w1")

	if c := secureboot.ClusterCondition("SecureBoot", machines); c.Status != metav1.ConditionUnknown {
		t.Errorf("one unread machine: %+v", c)
	}

	delete(machines, "w2")

	if c := secureboot.ClusterCondition("SecureBoot", machines); c.Status != metav1.ConditionTrue || c.Reason != "AllNodesConformant" {
		t.Errorf("all conformant: %+v", c)
	}

	if c := secureboot.ClusterCondition("SecureBoot", nil); c.Status != metav1.ConditionUnknown {
		t.Errorf("no machines: %+v", c)
	}
}

