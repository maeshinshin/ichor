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

func configPatch(name string, cfg *typedconfig.Config) *ichorv1alpha1.TalosConfigPatch {
	return &ichorv1alpha1.TalosConfigPatch{
		Name: name, Namespace: dryRenderNS,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      "cluster",
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: "Config", Config: cfg}},
		},
	}
}

func TestTalosConfigPatchDryRender(t *testing.T) {
	ctx := context.Background()
	v := dryRenderValidator(t)

	t.Run("a v1alpha1 apiServer block under the multi-document contract is refused with the render error", func(t *testing.T) {
		p := configPatch("api", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			ClusterConfig: &typedconfig.ClusterConfig{APIServerConfig: &typedconfig.APIServerConfig{
				ExtraArgsConfig: map[string][]string{"audit-log-maxage": {"3"}},
			}},
		})

		_, err := v.ValidateCreate(ctx, p)
		if err == nil || !strings.Contains(err.Error(), ".cluster.apiServer") || !strings.Contains(err.Error(), `"cp1"`) {
			t.Fatalf("want the render error naming cp1, got %v", err)
		}

		if _, err := v.ValidateUpdate(ctx, p, p); err == nil {
			t.Fatal("update must be refused too")
		}
	})

	t.Run("a sparse patch that renders is admitted", func(t *testing.T) {
		p := configPatch("ok", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			ClusterConfig: &typedconfig.ClusterConfig{ExtraManifests: []string{"https://example.invalid/x.yaml"}},
		})

		if _, err := v.ValidateCreate(ctx, p); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a patch that applies to no machine is admitted", func(t *testing.T) {
		p := configPatch("none", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			ClusterConfig: &typedconfig.ClusterConfig{APIServerConfig: &typedconfig.APIServerConfig{
				ExtraArgsConfig: map[string][]string{"x": {"y"}},
			}},
		})
		p.Spec.Layer = "role"
		p.Spec.Target = &ichorv1alpha1.TalosConfigPatchTarget{Role: "worker"}

		if _, err := v.ValidateCreate(ctx, p); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a machine that already fails to render does not block the patch", func(t *testing.T) {
		broken := configPatch("broken", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			ClusterConfig: &typedconfig.ClusterConfig{APIServerConfig: &typedconfig.APIServerConfig{
				ExtraArgsConfig: map[string][]string{"x": {"y"}},
			}},
		})
		v2 := dryRenderValidator(t, broken)

		p := configPatch("fix", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			ClusterConfig: &typedconfig.ClusterConfig{ExtraManifests: []string{"https://example.invalid/x.yaml"}},
		})

		if _, err := v2.ValidateCreate(ctx, p); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})

	t.Run("a cluster without a secrets bundle is not checked", func(t *testing.T) {
		v3 := dryRenderValidator(t)
		v3.SecretNamespace = "elsewhere"

		p := configPatch("nb", &typedconfig.Config{
			ConfigVersion: dryRenderVersion,
			ClusterConfig: &typedconfig.ClusterConfig{APIServerConfig: &typedconfig.APIServerConfig{
				ExtraArgsConfig: map[string][]string{"x": {"y"}},
			}},
		})

		if _, err := v3.ValidateCreate(ctx, p); err != nil {
			t.Fatalf("want admitted, got %v", err)
		}
	})
}

func resolverPatch(name string, addrs ...string) *ichorv1alpha1.TalosConfigPatch {
	r := &typedconfig.ResolverConfigV1Alpha1{}
	for _, a := range addrs {
		r.ResolverNameservers = append(r.ResolverNameservers, &typedconfig.NameserverConfig{Address: a})
	}

	return &ichorv1alpha1.TalosConfigPatch{
		Name: name, Namespace: dryRenderNS,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      "cluster",
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: "ResolverConfig", ResolverConfig: r}},
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

// The CRD only bounds a document to kind plus one field; the webhook refuses the one whose field is not the
// field kind names, naming the kind, the field it needs and the field it has.
func TestTalosConfigPatchDocumentKindNamesItsField(t *testing.T) {
	ctx := context.Background()
	v := dryRenderValidator(t)

	ok := ichorv1alpha1.TalosConfigPatchDocument{
		Kind: "SysctlConfig", SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{"net.ipv4.ip_forward": "1"}},
	}

	for name, tc := range map[string]struct {
		doc  ichorv1alpha1.TalosConfigPatchDocument
		want string
	}{
		"another field is set": {
			ichorv1alpha1.TalosConfigPatchDocument{Kind: "SysctlConfig", KernelModuleConfig: &typedconfig.KernelModuleConfigV1Alpha1{MetaName: "btrfs"}},
			`spec.documents[1]: kind "SysctlConfig" needs the field sysctlConfig alone, but the document sets kernelModuleConfig`,
		},
		"no field is set": {
			ichorv1alpha1.TalosConfigPatchDocument{Kind: "KernelModuleConfig"},
			`spec.documents[1]: kind "KernelModuleConfig" needs the field kernelModuleConfig, but the document sets no field`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := configPatch("p", nil)
			p.Spec.Documents = []ichorv1alpha1.TalosConfigPatchDocument{ok, tc.doc}

			if _, err := v.ValidateCreate(ctx, p); err == nil || err.Error() != tc.want {
				t.Errorf("create: want %q, got %v", tc.want, err)
			}

			good := configPatch("p", nil)
			good.Spec.Documents = []ichorv1alpha1.TalosConfigPatchDocument{ok, ok}

			if _, err := v.ValidateUpdate(ctx, good, p); err == nil || err.Error() != tc.want {
				t.Errorf("update: want %q, got %v", tc.want, err)
			}
		})
	}

	p := configPatch("p", nil)
	p.Spec.Documents = []ichorv1alpha1.TalosConfigPatchDocument{ok}

	if _, err := v.ValidateCreate(ctx, p); err != nil {
		t.Errorf("a document whose kind names its field was refused: %v", err)
	}
}
