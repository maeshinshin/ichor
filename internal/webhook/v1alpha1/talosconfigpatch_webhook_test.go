package v1alpha1

import (
	"context"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	dryRenderNS       = "ns"
	dryRenderSecretNS = "ichor-system"
	dryRenderFalse    = "false"
	dryRenderVersion  = "v1alpha1"
)

func dryRenderValidator(t *testing.T, objs ...runtime.Object) *TalosConfigPatchCustomValidator {
	t.Helper()

	contract, err := coreconfig.ParseContractFromVersion(testVersionContract)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}

	scheme := schemeOf(t, corev1.AddToScheme)

	cluster := &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: dryRenderNS,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.5"},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: "v1.36.0",
			VersionContract:   testVersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "bundle"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: dryRenderFalse},
		},
	}
	secret := &corev1.Secret{
		Name: "bundle", Namespace: dryRenderSecretNS,
		Data: map[string][]byte{secretsbundle.SecretKey: raw},
	}
	cp := &ichorv1alpha1.TalosMachine{
		Name: "cp1", Namespace: dryRenderNS,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: "c"},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "u1"},
			Endpoint:      "10.0.0.10:50000",
			Role:          roleControlPlane,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(append(objs, cluster, secret, cp)...).Build()

	return &TalosConfigPatchCustomValidator{Client: c, SecretNamespace: dryRenderSecretNS}
}

func configPatch(name string, cfg *ichorconfig.Config) *ichorv1alpha1.TalosConfigPatch {
	return &ichorv1alpha1.TalosConfigPatch{
		Name: name, Namespace: dryRenderNS,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      "cluster",
			Config:     cfg,
		},
	}
}

// apiServerArgs is a setting of the API server, which a v1.14 machine takes in the KubeAPIServerConfig document.
func apiServerArgs(k string) *ichorconfig.Config {
	return &ichorconfig.Config{Cluster: &ichorconfig.Cluster{ApiServer: &ichorconfig.ClusterApiServer{
		ExtraArgs: map[string][]string{k: {"3"}},
	}}}
}

func extraManifest() *ichorconfig.Config {
	return &ichorconfig.Config{Cluster: &ichorconfig.Cluster{ExtraManifests: map[string]*ichorconfig.ClusterExtraManifestsEntry{
		"x": {Url: "https://example.invalid/x.yaml"},
	}}}
}

func TestTalosConfigPatchDryRender(t *testing.T) {
	ctx := context.Background()
	v := dryRenderValidator(t)

	t.Run("a patch that cannot render on a machine is refused with the render error", func(t *testing.T) {
		p := configPatch("bad", badSetting())

		_, err := v.ValidateCreate(ctx, p)
		if err == nil || !strings.Contains(err.Error(), `"cp1"`) {
			t.Fatalf("want the render error naming cp1, got %v", err)
		}

		if _, err := v.ValidateUpdate(ctx, p, p); err == nil {
			t.Fatal("update must be refused too")
		}
	})

	t.Run("a setting of the API server is admitted under the multi-document contract", func(t *testing.T) {
		if _, err := v.ValidateCreate(ctx, configPatch("api", apiServerArgs("audit-log-maxage"))); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a sparse patch that renders is admitted", func(t *testing.T) {
		if _, err := v.ValidateCreate(ctx, configPatch("ok", extraManifest())); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a patch that applies to no machine is admitted", func(t *testing.T) {
		p := configPatch("none", badSetting())
		p.Spec.Layer = "role"
		p.Spec.Target = &ichorv1alpha1.TalosConfigPatchTarget{Role: "worker"}

		if _, err := v.ValidateCreate(ctx, p); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a machine that already fails to render does not block the patch", func(t *testing.T) {
		v2 := dryRenderValidator(t, configPatch("broken", badSetting()))

		if _, err := v2.ValidateCreate(ctx, configPatch("fix", extraManifest())); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a cluster without a secrets bundle is not checked", func(t *testing.T) {
		v3 := dryRenderValidator(t)
		v3.SecretNamespace = "elsewhere"

		if _, err := v3.ValidateCreate(ctx, configPatch("nb", badSetting())); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})
}

func resolverPatch(name string, addrs ...string) *ichorv1alpha1.TalosConfigPatch {
	n := &ichorconfig.MachineNetwork{}
	for _, a := range addrs {
		n.Nameservers = append(n.Nameservers, &typedconfig.NameserverConfig{Address: a})
	}

	return &ichorv1alpha1.TalosConfigPatch{
		Name: name, Namespace: dryRenderNS,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      "cluster",
			Config:     &ichorconfig.Config{Machine: &ichorconfig.Machine{Network: n}},
		},
	}
}

func TestTalosConfigPatchResolverConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("a good ResolverConfig is admitted", func(t *testing.T) {
		if _, err := dryRenderValidator(t).ValidateCreate(ctx, resolverPatch("dns", "9.9.9.9")); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a nameserver that is not an address is refused", func(t *testing.T) {
		if _, err := dryRenderValidator(t).ValidateCreate(ctx, resolverPatch("bad", "not-an-ip")); err == nil {
			t.Fatal("want refused")
		}
	})

	t.Run("a machine that already renders a ResolverConfig from its spec refuses one", func(t *testing.T) {
		m := &ichorv1alpha1.TalosMachine{
			Name: "cp2", Namespace: dryRenderNS,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: "c"},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "u2"},
				Endpoint:      "10.0.0.11:50000",
				Role:          roleControlPlane,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
				Network:       &ichorv1alpha1.TalosMachineNetwork{MAC: "aa:bb:cc:dd:ee:ff", Addressing: "dhcp", DNSServers: []string{"1.1.1.1"}},
			},
		}

		_, err := dryRenderValidator(t, m).ValidateCreate(ctx, resolverPatch("dns", "9.9.9.9"))
		if err == nil || !strings.Contains(err.Error(), "spec.network.dnsServers") {
			t.Fatalf("want the dnsServers conflict, got %v", err)
		}
	})
}

// badSetting is a setting Talos refuses when it validates the rendered configuration.
func badSetting() *ichorconfig.Config {
	return &ichorconfig.Config{Machine: &ichorconfig.Machine{Kubelet: &ichorconfig.MachineKubelet{Image: "!!"}}}
}
