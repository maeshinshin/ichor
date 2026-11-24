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

	deps := adoptDeps{Seed: cpSeed, Target: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(node("cp", cpSeed), node("wk", wkIP)).Build()}
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
			node("cp", cpSeed), node("wk", wkHost), testDS("cilium", ciliumImage), ciliumCM(kprOff)).Build(),
		Preflight: func(_ context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
			rep := &cilium.PreflightReport{}
			if in.ConflictsOnly {
				rep.Conflicts = []cilium.Conflict{{Key: ipamModeKey, Live: poolMode, Level: cilium.Blocking}}
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

const (
	kprOff, poolMode, routeNative, routingModeKey = "false", "cluster-pool", "native", "routing-mode"
	ipamModeKey, ipamK8s                          = "mode", "kubernetes"
	tunnelValueKey, ipamValueKey, routingValueKey = "tunnelProtocol", "ipam", "routingMode"
)

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

	cm := ciliumCM(kprOff)
	cm.Data = data

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(testDS("cilium", ciliumImage), cm).Build()
}

// A foreign Cilium on geneve is taken over with spec.cni.tunnel=geneve, so the early check must not compare it
// with the default tunnel when --tunnel is absent.
func TestRefuseBlockingCiliumAcceptsLiveGeneveWithoutTunnelFlag(t *testing.T) {
	data := map[string]string{ciliumKPRKey: kprOff, ciliumTunnelKey: ciliumTunnelGeneve}
	deps := adoptDeps{
		Target:    foreignCiliumTarget(t, data),
		Preflight: ownedKeyPreflight(t, map[string]any{tunnelValueKey: ciliumTunnelGeneve, ipamValueKey: map[string]any{ipamModeKey: ipamK8s}}),
	}

	if err := refuseBlockingCilium(context.Background(), deps); err != nil {
		t.Fatalf("early check refused a live geneve Cilium: %v", err)
	}

	st := &adoptState{
		cluster: &ichorv1alpha1.TalosCluster{}, kubeProxy: kubeProxyEnabled, rep: &cniReport{Kind: detectedCilium},
	}
	if err := st.mapRunningCilium(factsFor(t, testDS("cilium", ciliumImage), &corev1.ConfigMap{
		Namespace: k8sFactsSystemNamespace, Name: ciliumConfigMapName, Data: data})); err != nil {
		t.Fatal(err)
	}

	if got := st.cluster.Spec.CNI.Tunnel; got != ciliumTunnelGeneve {
		t.Errorf("plan sets spec.cni.tunnel=%q; want geneve", got)
	}
}

// The early check and the plan's preflight answer one question, whether the live Cilium can be taken over:
// they must refuse exactly the same configs.
func TestRefuseBlockingCiliumAgreesWithPlanPreflight(t *testing.T) {
	k8sIPAM := map[string]any{ipamModeKey: ipamK8s}
	// The last row's existing TalosCluster has the unmanaged preset: the plan never takes that Cilium over.
	const unmanagedRow = "cluster-pool, unmanaged preset"

	tests := []struct {
		name string
		cm   map[string]string
		live map[string]any
	}{
		{ciliumTunnelVxlan, map[string]string{ciliumTunnelKey: ciliumTunnelVxlan}, map[string]any{tunnelValueKey: ciliumTunnelVxlan, ipamValueKey: k8sIPAM}},
		{ciliumTunnelGeneve, map[string]string{ciliumTunnelKey: ciliumTunnelGeneve}, map[string]any{tunnelValueKey: ciliumTunnelGeneve, ipamValueKey: k8sIPAM}},
		{"native routing", map[string]string{routingModeKey: routeNative}, map[string]any{routingValueKey: routeNative, ipamValueKey: k8sIPAM}},
		{poolMode, map[string]string{ciliumTunnelKey: ciliumTunnelVxlan},
			map[string]any{tunnelValueKey: ciliumTunnelVxlan, ipamValueKey: map[string]any{ipamModeKey: poolMode}}},
		{"geneve cluster-pool", map[string]string{ciliumTunnelKey: ciliumTunnelGeneve},
			map[string]any{tunnelValueKey: ciliumTunnelGeneve, ipamValueKey: map[string]any{ipamModeKey: poolMode}}},
		{unmanagedRow, map[string]string{ciliumTunnelKey: ciliumTunnelVxlan},
			map[string]any{tunnelValueKey: ciliumTunnelVxlan, ipamValueKey: map[string]any{ipamModeKey: poolMode}}},
	}

	for _, tt := range tests {
		for _, kp := range []string{kubeProxyEnabled, kubeProxyReplaced} {
			t.Run(tt.name+"/"+kp, func(t *testing.T) {
				tt.cm[ciliumKPRKey] = kprOff
				pre := ownedKeyPreflight(t, tt.live)

				existing := &ichorv1alpha1.TalosCluster{}
				if tt.name == unmanagedRow {
					existing.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged
				}

				edeps := adoptDeps{Target: foreignCiliumTarget(t, tt.cm), Preflight: pre}
				edeps.CNIUnmanaged = edeps.cniUnmanaged(existing)
				early := refuseBlockingCilium(context.Background(), edeps) != nil

				facts := factsFor(t, testDS("cilium", ciliumImage), &corev1.ConfigMap{
					Namespace: k8sFactsSystemNamespace, Name: ciliumConfigMapName, Data: tt.cm})
				st := &adoptState{cluster: &ichorv1alpha1.TalosCluster{}, kubeProxy: kp, rep: &cniReport{Kind: detectedCilium}}

				if err := st.mapRunningCilium(facts); err != nil {
					t.Fatal(err)
				}

				rep, err := st.preflight(context.Background(), adoptDeps{Preflight: pre}, nil)
				if err != nil {
					t.Fatal(err)
				}

				plan := rep.Err(true, false) != nil && existing.Spec.CNI.Preset != ichorv1alpha1.TalosClusterCNIPresetUnmanaged
				if early != plan {
					t.Errorf("early check refuses = %v, plan preflight refuses = %v", early, plan)
				}
			})
		}
	}
}

// A re-run against a TalosCluster whose spec already sets the unmanaged CNI preset, without --cni-unmanaged,
// is not refused over a Cilium Ichor will never take over.
func TestRediscoverSkipsCiliumCheckForUnmanagedPreset(t *testing.T) {
	cm := map[string]string{ciliumKPRKey: kprOff, routingModeKey: routeNative}
	deps := adoptDeps{
		Seed:   "192.0.2.9",
		Target: foreignCiliumTarget(t, cm),
		Preflight: ownedKeyPreflight(t, map[string]any{
			ipamValueKey: map[string]any{ipamModeKey: poolMode}, routingValueKey: routeNative}),
		DialTalos: func(context.Context, string) (talosConfigReader, error) { return nil, errors.New("dial reached") },
		ReadIdentity: func(context.Context, string) (controller.HardwareIdentity, error) {
			return controller.HardwareIdentity{}, nil
		},
	}
	cluster := &ichorv1alpha1.TalosCluster{}
	cluster.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged

	err := rediscover(context.Background(), deps, cluster, "ns", &[]*ichorv1alpha1.TalosMachine{}, nil)
	if err == nil || !strings.Contains(err.Error(), "dial reached") {
		t.Fatalf("err = %v; want the check skipped and the seed dialled", err)
	}
}
