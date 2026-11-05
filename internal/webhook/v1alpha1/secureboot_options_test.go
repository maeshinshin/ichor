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

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func sbSecurity(mode string) *ichorv1alpha1.TalosClusterSecurity {
	return &ichorv1alpha1.TalosClusterSecurity{SecureBoot: &ichorv1alpha1.TalosClusterSecureBoot{Mode: mode}}
}

func sbMachine(name string, sec *ichorv1alpha1.TalosMachineSecurity) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: name, Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: "c"},
			Role:          roleWorker,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			Endpoint:      "10.0.0.9:50000",
		},
		Status: ichorv1alpha1.TalosMachineStatus{Security: sec},
	}
}

func warned(warns []string, sub string) bool {
	for _, w := range warns {
		if strings.Contains(w, sub) {
			return true
		}
	}

	return false
}

// ADR 0123: Required on a cluster whose machines are not all SecureBoot needs a confirmation;
// everything else about SecureBoot and lockdown warns.
func TestSecureBootRequiredNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	scheme := schemeOf(t)

	validator := func(objs ...*ichorv1alpha1.TalosMachine) *TalosClusterCustomValidator {
		b := fake.NewClientBuilder().WithScheme(scheme)
		for _, o := range objs {
			b = b.WithObjects(o)
		}

		return &TalosClusterCustomValidator{APIReader: b.Build()}
	}

	on := &ichorv1alpha1.TalosMachineSecurity{SecureBoot: true, BootedWithUKI: true}
	off := &ichorv1alpha1.TalosMachineSecurity{BootedWithUKI: true}

	t.Run("refused while an approved machine does not run SecureBoot, naming it and the annotation", func(t *testing.T) {
		_, err := validator(sbMachine("m-off", off), sbMachine("m-on", on)).
			ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(sbSecurity("Required")))
		if err == nil {
			t.Fatal("want a refusal")
		}

		for _, want := range []string{ichorv1alpha1.TalosClusterSecureBootConfirmAnnotation, "m-off", "reset"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q missing from %v", want, err)
			}
		}

		if strings.Contains(err.Error(), "m-on") {
			t.Errorf("names a machine that runs SecureBoot: %v", err)
		}
	})

	t.Run("a machine not read yet counts as not running it", func(t *testing.T) {
		if _, err := validator(sbMachine("m", nil)).ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(sbSecurity("Required"))); err == nil {
			t.Fatal("want a refusal")
		}
	})

	t.Run("the annotation confirms it", func(t *testing.T) {
		c := secTestCluster(sbSecurity("Required"))
		c.Annotations = map[string]string{ichorv1alpha1.TalosClusterSecureBootConfirmAnnotation: annotationTrue}

		if _, err := validator(sbMachine("m", off)).ValidateUpdate(ctx, secTestCluster(nil), c); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("all machines on SecureBoot, or none yet, need no confirmation", func(t *testing.T) {
		if _, err := validator(sbMachine("m", on)).ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(sbSecurity("Required"))); err != nil {
			t.Fatal(err)
		}

		if _, err := validator().ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(sbSecurity("Required"))); err != nil {
			t.Fatal(err)
		}

		if _, err := validator().ValidateCreate(ctx, secTestCluster(sbSecurity("Required"))); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an unrelated edit of a Required cluster is not asked again", func(t *testing.T) {
		old := secTestCluster(sbSecurity("Required"))
		changed := old.DeepCopy()
		changed.Spec.KubernetesVersion = secNewKubeVersion

		if _, err := validator(sbMachine("m", off)).ValidateUpdate(ctx, old, changed); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Preferred never refuses; it warns about the machines without SecureBoot", func(t *testing.T) {
		warns, err := validator(sbMachine("m-off", off)).ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(sbSecurity("Preferred")))
		if err != nil {
			t.Fatal(err)
		}

		if !warned(warns, "m-off") {
			t.Errorf("warnings %v do not name the machine", warns)
		}
	})
}

func TestSecureBootWarnings(t *testing.T) {
	ctx := context.Background()
	v := &TalosClusterCustomValidator{APIReader: fake.NewClientBuilder().WithScheme(schemeOf(t)).Build()}

	t.Run("a lockdown expectation without SecureBoot says it has to be built into the boot asset", func(t *testing.T) {
		warns, err := v.ValidateCreate(ctx, secTestCluster(&ichorv1alpha1.TalosClusterSecurity{KernelLockdown: "confidentiality"}))
		if err != nil {
			t.Fatal(err)
		}

		if !warned(warns, "schematic") {
			t.Errorf("warnings %v", warns)
		}
	})

	t.Run("confidentiality with SecureBoot also needs the schematic, integrity does not", func(t *testing.T) {
		sec := sbSecurity("Preferred")
		sec.KernelLockdown = "confidentiality"

		warns, _ := v.ValidateCreate(ctx, secTestCluster(sec))
		if !warned(warns, "lockdown=confidentiality") {
			t.Errorf("warnings %v", warns)
		}

		sec.KernelLockdown = "integrity"

		warns, _ = v.ValidateCreate(ctx, secTestCluster(sec))
		if len(warns) != 0 {
			t.Errorf("integrity under SecureBoot is Talos's default, got warnings %v", warns)
		}
	})

	t.Run("a TPM key that requires SecureBoot with the mode Off warns", func(t *testing.T) {
		yes := true
		sec := &ichorv1alpha1.TalosClusterSecurity{
			DiskEncryption: &ichorv1alpha1.TalosClusterDiskEncryption{Type: "tpm", RequireSecureBoot: &yes},
		}

		warns, err := v.ValidateCreate(ctx, secTestCluster(sec))
		if err != nil {
			t.Fatal(err)
		}

		if !warned(warns, "requireSecureBoot") {
			t.Errorf("warnings %v", warns)
		}

		sec.SecureBoot = &ichorv1alpha1.TalosClusterSecureBoot{Mode: "Required"}

		if warns, _ = v.ValidateCreate(ctx, secTestCluster(sec)); warned(warns, "requireSecureBoot") {
			t.Errorf("warned with the mode Required: %v", warns)
		}
	})
}

// A Required cluster warns about an explicit installer image that is not the SecureBoot variant.
func TestSecureBootInstallerImageWarning(t *testing.T) {
	ctx := context.Background()

	cluster := secTestCluster(sbSecurity("Required"))
	cluster.Namespace = testNamespace

	v := &TalosMachineCustomValidator{APIReader: fake.NewClientBuilder().WithScheme(schemeOf(t)).WithObjects(cluster).Build()}

	update := func(image string) []string {
		old := sbMachine("m", nil)
		old.Spec.Install = &ichorv1alpha1.TalosMachineInstall{Disk: ichorv1alpha1.TalosMachineDisk{DevPath: "/dev/vda"}}
		next := old.DeepCopy()
		next.Spec.Install.Image = image

		warns, err := v.ValidateUpdate(ctx, old, next)
		if err != nil {
			t.Fatal(err)
		}

		return warns
	}

	if warns := update("factory.talos.dev/metal-installer/abc:v1.14.5"); !warned(warns, "metal-installer-secureboot") {
		t.Errorf("stock installer under Required: %v", warns)
	}

	if warns := update("factory.talos.dev/metal-installer-secureboot/abc:v1.14.5"); warned(warns, "metal-installer-secureboot") {
		t.Errorf("SecureBoot installer warned: %v", warns)
	}

	if warns := update(""); warned(warns, "metal-installer-secureboot") {
		t.Errorf("no image warned: %v", warns)
	}
}
