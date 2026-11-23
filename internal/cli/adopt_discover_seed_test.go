package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// A member lists every address of the node, Cilium's cluster-pool cilium_host address among them (10.0.0.0/8 sorts
// first). Adopt must dial and record the seed address for the seed's member and the Node's InternalIP for the others.
func TestDiscoverMachinesPicksNodeAddressesNotCiliumHostOnes(t *testing.T) {
	const cpSeed, wkIP, cpHost, wkHost = "10.244.2.87", "10.244.2.88", "10.0.0.187", "10.0.1.12"

	node := func(name, ip string) *corev1.Node {
		return &corev1.Node{
			Name:   name,
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}},
		}
	}

	deps := adoptDeps{Seed: cpSeed, Target: fake.NewClientBuilder().WithObjects(node("cp", cpSeed), node("wk", wkIP)).Build()}
	deps.DialTalos = func(context.Context, string) (talosConfigReader, error) {
		return &membersReader{members: []ClusterMember{
			{Hostname: "cp", Addresses: []string{cpHost, cpSeed}, Role: roleControlPlane},
			{Hostname: "wk", Addresses: []string{wkHost, wkIP}, Role: testRoleWorker},
		}}, nil
	}
	deps.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "u-" + address}, nil
	}

	machines, _, err := discoverMachines(context.Background(), deps, "c", "ns")
	if err != nil {
		t.Fatal(err)
	}

	if got := []string{machines[0].Spec.Endpoint, machines[1].Spec.Endpoint}; got[0] != cpSeed || got[1] != wkIP {
		t.Fatalf("endpoints %v; want [%s %s]", got, cpSeed, wkIP)
	}
}

// A Cilium on ipam.mode cluster-pool is refused whatever the nodes' addresses are: the refusal reads only the
// Kubernetes API, so it comes before adopt dials any member (a worker's Node address may sit in the pod pool).
func TestDiscoverMachinesRefusesClusterPoolBeforeDialingMembers(t *testing.T) {
	const cpSeed, wkHost = "10.244.2.87", "10.0.1.12"

	node := func(name, ip string) *corev1.Node {
		return &corev1.Node{
			Name:   name,
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}},
		}
	}

	var identityReads int

	deps := adoptDeps{
		Seed: cpSeed,
		Target: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
			node("cp", cpSeed), node("wk", wkHost), testDS("cilium", ciliumImage), ciliumCM("false")).Build(),
		Preflight: func(_ context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
			rep := &cilium.PreflightReport{}
			if in.ConflictsOnly {
				rep.Conflicts = []cilium.Conflict{{Key: "mode", Live: "cluster-pool", Level: cilium.Blocking}}
			}

			return rep, nil
		},
		DialTalos: func(context.Context, string) (talosConfigReader, error) {
			return &membersReader{members: []ClusterMember{
				{Hostname: "cp", Addresses: []string{cpSeed}, Role: roleControlPlane},
				{Hostname: "wk", Addresses: []string{wkHost}, Role: testRoleWorker},
			}}, nil
		},
		ReadIdentity: func(context.Context, string) (controller.HardwareIdentity, error) {
			identityReads++

			return controller.HardwareIdentity{}, errors.New("i/o timeout")
		},
	}

	_, _, err := discoverMachines(context.Background(), deps, "c", "ns")
	if err == nil || !strings.Contains(err.Error(), "cannot be taken over") {
		t.Fatalf("err = %v; want the ipam.mode refusal", err)
	}

	if identityReads != 0 {
		t.Errorf("adopt read %d members' hardware identity before refusing", identityReads)
	}
}

// ownedKeyPreflight answers like cilium.Preflight does for a Cilium whose live values are live: the owned-key
// conflicts for the tunnel and kube-proxy mode it is given.
func ownedKeyPreflight(t *testing.T, live map[string]any) ciliumPreflight {
	t.Helper()

	return func(_ context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
		c, err := cilium.OwnedKeyConflicts(live, in.KubeProxy, in.Tunnel)

		return &cilium.PreflightReport{Conflicts: c}, err
	}
}

func foreignCiliumTarget(t *testing.T, data map[string]string) client.Client {
	t.Helper()

	cm := ciliumCM("false")
	cm.Data = data

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(testDS("cilium", ciliumImage), cm).Build()
}

// A foreign Cilium on geneve is taken over with spec.cni.tunnel=geneve, so the early check must not compare it
// with the default tunnel when --tunnel is absent.
func TestRefuseBlockingCiliumAcceptsLiveGeneveWithoutTunnelFlag(t *testing.T) {
	data := map[string]string{"kube-proxy-replacement": "false", ciliumTunnelKey: ciliumTunnelGeneve}
	deps := adoptDeps{
		Target:    foreignCiliumTarget(t, data),
		Preflight: ownedKeyPreflight(t, map[string]any{"tunnelProtocol": "geneve", "ipam": map[string]any{"mode": "kubernetes"}}),
	}

	if err := refuseBlockingCilium(context.Background(), deps); err != nil {
		t.Fatalf("early check refused a live geneve Cilium: %v", err)
	}

	st := &adoptState{
		cluster: &ichorv1alpha1.TalosCluster{}, kubeProxy: kubeProxyEnabled, rep: &cniReport{Kind: detectedCilium},
	}
	if err := st.mapRunningCilium(factsFor(t, testDS("cilium", ciliumImage), &corev1.ConfigMap{
		Namespace: k8sFactsSystemNamespace, Name: "cilium-config", Data: data})); err != nil {
		t.Fatal(err)
	}

	if got := st.cluster.Spec.CNI.Tunnel; got != ciliumTunnelGeneve {
		t.Errorf("plan sets spec.cni.tunnel=%q; want geneve", got)
	}
}

// The early check and the plan's preflight answer one question, whether the live Cilium can be taken over:
// they must refuse exactly the same configs.
func TestRefuseBlockingCiliumAgreesWithPlanPreflight(t *testing.T) {
	k8sIPAM := map[string]any{"mode": "kubernetes"}
	tests := []struct {
		name string
		cm   map[string]string
		live map[string]any
	}{
		{"vxlan", map[string]string{ciliumTunnelKey: "vxlan"}, map[string]any{"tunnelProtocol": "vxlan", "ipam": k8sIPAM}},
		{"geneve", map[string]string{ciliumTunnelKey: "geneve"}, map[string]any{"tunnelProtocol": "geneve", "ipam": k8sIPAM}},
		{"native routing", map[string]string{"routing-mode": "native"}, map[string]any{"routingMode": "native", "ipam": k8sIPAM}},
		{"cluster-pool", map[string]string{ciliumTunnelKey: "vxlan"},
			map[string]any{"tunnelProtocol": "vxlan", "ipam": map[string]any{"mode": "cluster-pool"}}},
		{"geneve cluster-pool", map[string]string{ciliumTunnelKey: "geneve"},
			map[string]any{"tunnelProtocol": "geneve", "ipam": map[string]any{"mode": "cluster-pool"}}},
	}

	for _, tt := range tests {
		for _, kp := range []string{kubeProxyEnabled, kubeProxyReplaced} {
			t.Run(tt.name+"/"+kp, func(t *testing.T) {
				tt.cm["kube-proxy-replacement"] = "false"
				pre := ownedKeyPreflight(t, tt.live)

				early := refuseBlockingCilium(context.Background(), adoptDeps{Target: foreignCiliumTarget(t, tt.cm), Preflight: pre}) != nil

				facts := factsFor(t, testDS("cilium", ciliumImage), &corev1.ConfigMap{
					Namespace: k8sFactsSystemNamespace, Name: "cilium-config", Data: tt.cm})
				st := &adoptState{cluster: &ichorv1alpha1.TalosCluster{}, kubeProxy: kp, rep: &cniReport{Kind: detectedCilium}}

				if err := st.mapRunningCilium(facts); err != nil {
					t.Fatal(err)
				}

				rep, err := st.preflight(context.Background(), adoptDeps{Preflight: pre}, nil)
				if err != nil {
					t.Fatal(err)
				}

				if plan := rep.Err(true, false) != nil; early != plan {
					t.Errorf("early check refuses = %v, plan preflight refuses = %v", early, plan)
				}
			})
		}
	}
}
