package cli

import (
	"io"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// testCertSAN is the extra name the cert-SAN tests pass with --cert-san.
const testCertSAN = "ichor.lan"

// renderFirstControlPlane renders the first control plane that init builds from in, the way
// runInit does: candidatePatches (the bootstrap patch included), then render.Render.
func renderFirstControlPlane(t *testing.T, in initPlanInput, nodeAddress string) coreconfig.Provider {
	t.Helper()

	cluster, err := buildInitPlan(in, io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	cniBoot, err := buildCNIBootstrap(cilium.New().WithControlPlanes(3), cluster.Name, cluster.Namespace, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	machine := &ichorv1alpha1.TalosMachine{
		Name:      "uuid-first",
		Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-first"},
			Endpoint:   nodeAddress,
			Role:       roleControlPlane,
		},
	}
	candidates := candidatePatches(cluster, machine, cniBoot, nodeAddress, diskRoleOptions{})

	contract, err := coreconfig.ParseContractFromVersion(in.VersionContract)
	if err != nil {
		t.Fatalf("ParseContractFromVersion: %v", err)
	}

	bundle, err := secrets.NewBundle(secrets.NewClock(), contract)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	out, _, err := render.Render(cluster, machine, bundle, candidates, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	cfg, err := configloader.NewFromBytes(out)
	if err != nil {
		t.Fatalf("configloader: %v", err)
	}

	return cfg
}

func TestCertSAN_ReachesTheClusterSpec(t *testing.T) {
	in := validInitPlanInput()
	in.CertSANs = []string{testCertSAN}

	cluster, err := buildInitPlan(in, io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if !slices.Equal(cluster.Spec.CertSANs, []string{testCertSAN}) {
		t.Errorf("spec.certSANs = %v, want the flag's value", cluster.Spec.CertSANs)
	}
}

// TestCertSAN_RendersIntoBothCertificates pins the reach of one --cert-san value: it lands in the
// Talos API certificate (machine.certSANs) and in the kube-apiserver one (KubeAPIServerConfig
// certExtraSANs), and the address the bootstrap patch sets stays in machine.certSANs beside it.
func TestCertSAN_RendersIntoBothCertificates(t *testing.T) {
	in := validInitPlanInput()
	in.CertSANs = []string{testCertSAN}

	cfg := renderFirstControlPlane(t, in, "192.168.0.201")

	machineSANs := cfg.Machine().Security().CertSANs()
	if !slices.Contains(machineSANs, testCertSAN) {
		t.Errorf("machine.certSANs = %v, want ichor.lan from --cert-san", machineSANs)
	}

	if !slices.Contains(machineSANs, "192.168.0.201") {
		t.Errorf("machine.certSANs = %v, lost the bootstrap patch's node address", machineSANs)
	}

	if apiSANs := cfg.K8sAPIServerConfig().CertSANs(); !slices.Contains(apiSANs, testCertSAN) {
		t.Errorf("kube-apiserver certExtraSANs = %v, want ichor.lan from --cert-san", apiSANs)
	}
}
