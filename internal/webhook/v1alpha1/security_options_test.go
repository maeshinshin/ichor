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
	"errors"
	"net/netip"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	secConfirmed      = "true"
	secNewKubeVersion = "v1.36.9"
)

func secTestCluster(s *ichorv1alpha1.TalosClusterSecurity) *ichorv1alpha1.TalosCluster {
	c := newTestCluster()
	c.Name = "c"
	c.Spec.Security = s

	return c
}

func fixedSource(addr string) SourceResolver {
	return func(context.Context, string) (netip.Addr, error) { return netip.MustParseAddr(addr), nil }
}

func firewallSecurity() *ichorv1alpha1.TalosClusterSecurity {
	return &ichorv1alpha1.TalosClusterSecurity{Firewall: &ichorv1alpha1.TalosClusterFirewall{
		Enabled: true, ControllerCIDRs: []string{"192.168.10.0/24"}, NodeCIDRs: []string{"10.0.0.0/24"},
	}}
}

func TestSecurityFirewallAdmission(t *testing.T) {
	ctx := context.Background()

	t.Run("a controller inside controllerCIDRs is admitted", func(t *testing.T) {
		v := &TalosClusterCustomValidator{ControllerSource: fixedSource("192.168.10.9")}
		if _, err := v.ValidateCreate(ctx, secTestCluster(firewallSecurity())); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a controller outside controllerCIDRs is refused, on create and update", func(t *testing.T) {
		v := &TalosClusterCustomValidator{ControllerSource: fixedSource("172.31.0.4")}

		_, err := v.ValidateCreate(ctx, secTestCluster(firewallSecurity()))
		if err == nil || !strings.Contains(err.Error(), "172.31.0.4") || !strings.Contains(err.Error(), "controllerCIDRs") {
			t.Fatalf("want a refusal naming the controller address, got %v", err)
		}

		if _, err := v.ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(firewallSecurity())); err == nil {
			t.Fatal("enabling the firewall on an existing cluster must be checked too")
		}
	})

	t.Run("an address that cannot be found is not guessed at", func(t *testing.T) {
		v := &TalosClusterCustomValidator{ControllerSource: func(context.Context, string) (netip.Addr, error) {
			return netip.Addr{}, errors.New("no route")
		}}

		if _, err := v.ValidateCreate(ctx, secTestCluster(firewallSecurity())); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("enabled without sources is refused", func(t *testing.T) {
		v := &TalosClusterCustomValidator{ControllerSource: fixedSource("192.168.10.9")}
		s := firewallSecurity()
		s.Firewall.NodeCIDRs = nil

		if _, err := v.ValidateCreate(ctx, secTestCluster(s)); err == nil {
			t.Fatal("want an error")
		}
	})

	t.Run("a cluster without the option never resolves the controller address", func(t *testing.T) {
		v := &TalosClusterCustomValidator{ControllerSource: func(context.Context, string) (netip.Addr, error) {
			t.Fatal("resolver called")

			return netip.Addr{}, nil
		}}

		if _, err := v.ValidateCreate(ctx, secTestCluster(nil)); err != nil {
			t.Fatal(err)
		}

		if _, err := v.ValidateCreate(ctx, secTestCluster(&ichorv1alpha1.TalosClusterSecurity{Firewall: &ichorv1alpha1.TalosClusterFirewall{}})); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSecurityContractAndKMS(t *testing.T) {
	ctx := context.Background()
	v := &TalosClusterCustomValidator{}

	old := secTestCluster(&ichorv1alpha1.TalosClusterSecurity{PodSecurity: &ichorv1alpha1.TalosClusterPodSecurity{Enforce: "restricted"}})
	old.Spec.VersionContract = "v1.12"

	if _, err := v.ValidateCreate(ctx, old); err == nil || !strings.Contains(err.Error(), "v1.14") {
		t.Fatalf("podSecurity on v1.12 must be refused, got %v", err)
	}

	kms := secTestCluster(&ichorv1alpha1.TalosClusterSecurity{DiskEncryption: &ichorv1alpha1.TalosClusterDiskEncryption{
		Type: "kms", KMSEndpoint: "grpc://kms:4443",
	}})
	if _, err := v.ValidateCreate(ctx, kms); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plaintext KMS must be refused, got %v", err)
	}
}

func TestSecurityDiskEncryptionNeedsConfirmation(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	machine := &ichorv1alpha1.TalosMachine{
		Name: "m1", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: "c"},
			Role:          roleControlPlane,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}

	enc := func(typ string) *ichorv1alpha1.TalosClusterSecurity {
		return &ichorv1alpha1.TalosClusterSecurity{DiskEncryption: &ichorv1alpha1.TalosClusterDiskEncryption{Type: typ}}
	}

	withMachines := &TalosClusterCustomValidator{APIReader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).Build()}
	empty := &TalosClusterCustomValidator{APIReader: fake.NewClientBuilder().WithScheme(scheme).Build()}

	t.Run("a cluster with approved machines is refused without the annotation", func(t *testing.T) {
		_, err := withMachines.ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(enc("tpm")))
		if err == nil || !strings.Contains(err.Error(), ichorv1alpha1.TalosClusterDiskEncryptionConfirmAnnotation) {
			t.Fatalf("want a refusal naming the annotation, got %v", err)
		}
	})

	t.Run("the annotation confirms it", func(t *testing.T) {
		c := secTestCluster(enc("tpm"))
		c.Annotations = map[string]string{ichorv1alpha1.TalosClusterDiskEncryptionConfirmAnnotation: secConfirmed}

		if _, err := withMachines.ValidateUpdate(ctx, secTestCluster(nil), c); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a cluster without machines needs no confirmation", func(t *testing.T) {
		if _, err := empty.ValidateUpdate(ctx, secTestCluster(nil), secTestCluster(enc("nodeID"))); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an unrelated edit of an encrypted cluster is not asked again", func(t *testing.T) {
		old := secTestCluster(enc("tpm"))
		changed := old.DeepCopy()
		changed.Spec.KubernetesVersion = secNewKubeVersion

		if _, err := withMachines.ValidateUpdate(ctx, old, changed); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("changing the key type is asked again", func(t *testing.T) {
		if _, err := withMachines.ValidateUpdate(ctx, secTestCluster(enc("tpm")), secTestCluster(enc("nodeID"))); err == nil {
			t.Fatal("want a refusal")
		}
	})
}

func TestTalosAPIAccessInPatch(t *testing.T) {
	ctx := context.Background()
	on := true

	patch := func(roles ...string) *ichorv1alpha1.TalosConfigPatch {
		return configPatch("access", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			MachineConfig: &typedconfig.MachineConfig{MachineFeatures: &typedconfig.FeaturesConfig{
				KubernetesTalosAPIAccessConfig: &typedconfig.KubernetesTalosAPIAccessConfig{
					AccessEnabled: &on, AccessAllowedRoles: roles, AccessAllowedKubernetesNamespaces: []string{"ops"},
				},
			}},
		})
	}

	holder := func(allowed bool, err error) BreakGlassCheck {
		return func(context.Context, string, string, string) (bool, error) { return allowed, err }
	}

	t.Run("os:admin is refused", func(t *testing.T) {
		v := dryRenderValidator(t)
		v.BreakGlass = holder(false, nil)

		_, err := v.ValidateCreate(ctx, patch("os:reader", "os:admin"))
		if err == nil || !strings.Contains(err.Error(), "os:admin") || !strings.Contains(err.Error(), BreakGlassVerb) {
			t.Fatalf("want a refusal, got %v", err)
		}

		if _, err := v.ValidateUpdate(ctx, patch("os:reader"), patch("os:admin")); err == nil {
			t.Fatal("update must be refused too")
		}
	})

	t.Run("without a break-glass check os:admin is refused", func(t *testing.T) {
		if _, err := dryRenderValidator(t).ValidateCreate(ctx, patch("os:admin")); err == nil {
			t.Fatal("want a refusal")
		}
	})

	t.Run("a failed break-glass check refuses", func(t *testing.T) {
		v := dryRenderValidator(t)
		v.BreakGlass = holder(false, errors.New("boom"))

		if _, err := v.ValidateCreate(ctx, patch("os:admin")); err == nil {
			t.Fatal("want a refusal")
		}
	})

	t.Run("the break-glass verb admits it with a warning", func(t *testing.T) {
		v := dryRenderValidator(t)
		v.BreakGlass = holder(true, nil)

		warnings, err := v.ValidateCreate(ctx, patch("os:admin"))
		if err != nil {
			t.Fatal(err)
		}

		if len(warnings) != 1 || !strings.Contains(warnings[0], "ops") {
			t.Fatalf("want one warning naming the namespace, got %v", warnings)
		}
	})

	t.Run("os:reader is admitted with a warning", func(t *testing.T) {
		v := dryRenderValidator(t)

		warnings, err := v.ValidateCreate(ctx, patch("os:reader"))
		if err != nil || len(warnings) != 1 {
			t.Fatalf("want admitted with one warning, got %v, %v", warnings, err)
		}
	})

	t.Run("an update that leaves the documents alone is not re-checked", func(t *testing.T) {
		v := dryRenderValidator(t)
		old, same := patch("os:admin"), patch("os:admin")

		if _, err := v.ValidateUpdate(ctx, old, same); err != nil {
			t.Fatalf("stored patch must stay editable: %v", err)
		}
	})

	t.Run("disabled access is silent", func(t *testing.T) {
		p := patch("os:admin")
		off := false
		p.Spec.Documents[0].Config.MachineConfig.MachineFeatures.KubernetesTalosAPIAccessConfig.AccessEnabled = &off

		warnings, err := dryRenderValidator(t).ValidateCreate(ctx, p)
		if err != nil || len(warnings) != 0 {
			t.Fatalf("want silence, got %v, %v", warnings, err)
		}
	})
}
