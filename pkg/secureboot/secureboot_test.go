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

	lkNone      = ichorv1alpha1.KernelLockdownNone
	lkIntegrity = ichorv1alpha1.KernelLockdownIntegrity
	lkConf      = ichorv1alpha1.KernelLockdownConfidentiality
	condSB      = ichorv1alpha1.ConditionSecureBoot
	condKL      = ichorv1alpha1.ConditionKernelLockdown
)

type nodeSec = ichorv1alpha1.TalosMachineSecurity

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
		{"nothing, no secureboot", "talos.platform=metal", false, lkNone},
		{"secureboot is integrity by itself", "talos.platform=metal", true, lkIntegrity},
		{"explicit confidentiality", "talos.platform=metal lockdown=confidentiality", true, lkConf},
		{"explicit without secureboot", "lockdown=integrity talos.platform=metal", false, lkIntegrity},
		{"the last word wins", "lockdown=confidentiality lockdown=integrity", false, lkIntegrity},
		{"an unknown value is ignored", "lockdown=banana", true, lkIntegrity},
		{"explicit none", "lockdown=none", false, lkNone},
		{"not a prefix match", "xlockdown=confidentiality", false, lkNone},
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
		{lkNone, lkIntegrity, false},
		{lkIntegrity, lkIntegrity, true},
		{lkConf, lkIntegrity, true},
		{lkIntegrity, lkConf, false},
		{lkConf, lkConf, true},
		{"", lkIntegrity, false},
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

	uki := &nodeSec{BootedWithUKI: true}
	grub := &nodeSec{}
	running := &nodeSec{BootedWithUKI: true, SecureBoot: true}

	for _, tc := range []struct {
		name string
		sec  *ichorv1alpha1.TalosClusterSecurity
		node *nodeSec
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

		if got := secureboot.MachineConditions(nil, &nodeSec{SecureBoot: true}); len(got) != 0 {
			t.Errorf("got %v", got)
		}

		if got := secureboot.MachineConditions(security("Off", ""), nil); len(got) != 0 {
			t.Errorf("Off, got %v", got)
		}
	})

	t.Run("unread is Unknown", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Preferred", lkIntegrity), nil)
		if c := cond(got, condSB); c == nil || c.Status != metav1.ConditionUnknown {
			t.Errorf("SecureBoot = %+v", c)
		}

		if c := cond(got, condKL); c == nil || c.Status != metav1.ConditionUnknown {
			t.Errorf("KernelLockdown = %+v", c)
		}
	})

	t.Run("secureboot on", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Required", ""), &nodeSec{SecureBoot: true, BootedWithUKI: true})
		if c := cond(got, condSB); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "SecureBootOn" {
			t.Errorf("SecureBoot = %+v", c)
		}

		if cond(got, condKL) != nil {
			t.Error("KernelLockdown set without the option")
		}
	})

	t.Run("a GRUB install needs a reinstall", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Preferred", ""), &nodeSec{})
		c := cond(got, condSB)
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "NotUKI" {
			t.Fatalf("SecureBoot = %+v", c)
		}
	})

	t.Run("a UKI node with firmware SecureBoot off", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Required", ""), &nodeSec{BootedWithUKI: true})
		c := cond(got, condSB)
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != reasonOff {
			t.Fatalf("SecureBoot = %+v", c)
		}
	})

	t.Run("mode Off but the node runs SecureBoot", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("Off", lkIntegrity), &nodeSec{
			SecureBoot: true, BootedWithUKI: true, KernelLockdown: lkIntegrity,
		})
		if c := cond(got, condSB); c == nil || c.Status != metav1.ConditionFalse || c.Reason != "ModeOffButSecureBoot" {
			t.Errorf("SecureBoot = %+v", c)
		}

		if c := cond(got, condKL); c == nil || c.Status != metav1.ConditionTrue {
			t.Errorf("KernelLockdown = %+v", c)
		}
	})

	t.Run("lockdown below the expected level", func(t *testing.T) {
		t.Parallel()

		got := secureboot.MachineConditions(security("", lkConf), &nodeSec{
			SecureBoot: true, KernelLockdown: lkIntegrity,
		})

		c := cond(got, condKL)
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "LockdownBelowExpected" {
			t.Fatalf("KernelLockdown = %+v", c)
		}

		if cond(got, condSB) != nil {
			t.Error("SecureBoot set without the option")
		}
	})
}

func TestClusterCondition(t *testing.T) {
	t.Parallel()

	machines := map[string][]metav1.Condition{
		"cp1": {{Type: condSB, Status: metav1.ConditionTrue}},
		"w1":  {{Type: condSB, Status: metav1.ConditionFalse, Reason: reasonOff}},
		"w2":  {{Type: condSB, Status: metav1.ConditionUnknown}},
	}

	c := secureboot.ClusterCondition(condSB, machines)
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

	if c := secureboot.ClusterCondition(condSB, machines); c.Status != metav1.ConditionUnknown {
		t.Errorf("one unread machine: %+v", c)
	}

	delete(machines, "w2")

	c = secureboot.ClusterCondition(condSB, machines)
	if c.Status != metav1.ConditionTrue || c.Reason != "AllNodesConformant" {
		t.Errorf("all conformant: %+v", c)
	}

	if c := secureboot.ClusterCondition(condSB, nil); c.Status != metav1.ConditionUnknown {
		t.Errorf("no machines: %+v", c)
	}
}
