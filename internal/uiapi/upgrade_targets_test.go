package uiapi

import (
	"context"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// upgradeCluster is opCluster running the given versions, with Cilium at ciliumVersion ("" for none).
func upgradeCluster(talos, kubernetes, ciliumVersion string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{
		Name: opCluster, Namespace: opNS,
		Spec: ichorv1alpha1.TalosClusterSpec{TalosVersion: talos, KubernetesVersion: kubernetes},
	}

	if ciliumVersion != "" {
		c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{Detected: "cilium", DetectedVersion: ciliumVersion}
	}

	return c
}

// recordedLists is an IchorInstallation whose controller recorded the given lists.
func recordedLists(talos, kubernetes []string, errText string) *ichorv1alpha1.IchorInstallation {
	inst := ichorInstallation(ichorOld, ichorOld)
	inst.Status.UpgradeTargets = &ichorv1alpha1.IchorUpgradeTargets{Talos: talos, Kubernetes: kubernetes, Error: errText}

	return inst
}

func listUpgradeTargets(t *testing.T, srv *Server, kind pb.OperationKind) *pb.ListUpgradeTargetsResponse {
	t.Helper()

	res, err := srv.ListUpgradeTargets(context.Background(), connect.NewRequest(&pb.ListUpgradeTargetsRequest{
		Cluster: objectRef(opNS, opCluster), Kind: kind,
	}))
	if err != nil {
		t.Fatal(err)
	}

	return res.Msg
}

func TestListUpgradeTargetsGroupsTheTalosReleasesTheClusterCanTake(t *testing.T) {
	// Talos 1.12.0 on Kubernetes 1.34.2: 1.14.3 is the release set, 1.14.5 skips a minor, 1.15 is unknown.
	srv, _ := opServer(t,
		upgradeCluster("v1.12.0", "v1.34.2", "1.20.0"),
		recordedLists([]string{"v1.15.0", "v1.14.5", "v1.14.3", "v1.13.2", "v1.12.4", "v1.12.0"}, nil, ""),
	)

	m := listUpgradeTargets(t, srv, pb.OperationKind_OPERATION_KIND_TALOS)

	if !m.GetAvailable() || m.GetUnavailableReason() != "" {
		t.Fatalf("available = %v, reason = %q", m.GetAvailable(), m.GetUnavailableReason())
	}

	if !slices.Equal(m.GetRecommended(), []string{"v1.14.3"}) || !slices.Equal(m.GetPatch(), []string{"v1.12.4"}) ||
		!slices.Equal(m.GetNextMinor(), []string{"v1.13.2"}) {
		t.Fatalf("recommended %v, patch %v, next minor %v", m.GetRecommended(), m.GetPatch(), m.GetNextMinor())
	}

	if m.GetDefault() != "v1.14.3" {
		t.Fatalf("default = %q, want the recommended one", m.GetDefault())
	}
}

func TestListUpgradeTargetsGroupsTheKubernetesReleasesOfTheClusterTalosAndCilium(t *testing.T) {
	// Kubernetes 1.34.2 on Talos 1.14.3 with Cilium 1.20: 1.34.3 is a patch, 1.35.0 the next minor,
	// 1.36.0 and 1.37.1 are more than one minor up, and 1.37.1 is also not tested with Cilium 1.20.
	srv, _ := opServer(t,
		upgradeCluster("v1.14.3", "v1.34.2", "1.20.0"),
		recordedLists(nil, []string{"v1.37.1", "v1.36.0", "v1.35.0", "v1.34.3"}, ""),
	)

	m := listUpgradeTargets(t, srv, pb.OperationKind_OPERATION_KIND_KUBERNETES)

	if len(m.GetRecommended()) != 0 || !slices.Equal(m.GetPatch(), []string{"v1.34.3"}) ||
		!slices.Equal(m.GetNextMinor(), []string{"v1.35.0"}) {
		t.Fatalf("recommended %v, patch %v, next minor %v", m.GetRecommended(), m.GetPatch(), m.GetNextMinor())
	}

	if m.GetDefault() != "v1.34.3" {
		t.Fatalf("default = %q", m.GetDefault())
	}
}

func TestListUpgradeTargetsOffersNothingWhenCiliumIsUnknown(t *testing.T) {
	srv, _ := opServer(t,
		upgradeCluster("v1.14.3", "v1.34.2", ""),
		recordedLists(nil, []string{"v1.34.3", "v1.35.0"}, ""),
	)

	m := listUpgradeTargets(t, srv, pb.OperationKind_OPERATION_KIND_KUBERNETES)

	if !m.GetAvailable() || m.GetDefault() != "" || len(m.GetPatch())+len(m.GetNextMinor()) != 0 {
		t.Fatalf("an unknown Cilium should offer nothing: %v", m)
	}
}

func TestListUpgradeTargetsSaysWhyTheListIsNotThere(t *testing.T) {
	cases := map[string]struct {
		objs   []client.Object
		reason string
	}{
		"check off": {
			objs: func() []client.Object {
				inst := recordedLists(nil, []string{"v1.35.0"}, "")
				inst.Status.Effective = &ichorv1alpha1.IchorEffectiveSettings{
					UpdateCheck: &ichorv1alpha1.IchorEffectiveValue{Value: "false", Source: ichorv1alpha1.IchorSourceSpec},
				}

				return []client.Object{inst}
			}(),
			reason: "turn on the update check",
		},
		"first check not run": {
			objs:   []client.Object{ichorInstallation(ichorOld, ichorOld)},
			reason: "No release list yet",
		},
		"no installation": {
			objs:   nil,
			reason: "No release list yet",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := opServer(t, append([]client.Object{upgradeCluster("v1.14.3", "v1.34.2", "1.20.0")}, tc.objs...)...)

			m := listUpgradeTargets(t, srv, pb.OperationKind_OPERATION_KIND_TALOS)

			if m.GetAvailable() || !strings.Contains(m.GetUnavailableReason(), tc.reason) {
				t.Fatalf("available = %v, reason = %q, want %q", m.GetAvailable(), m.GetUnavailableReason(), tc.reason)
			}
		})
	}
}

func TestListUpgradeTargetsKeepsTheLastListWhenTheCheckFailed(t *testing.T) {
	srv, _ := opServer(t,
		upgradeCluster("v1.12.0", "v1.34.2", "1.20.0"),
		recordedLists([]string{"v1.12.4"}, nil, "listing the Kubernetes releases: registry unreachable"),
	)

	m := listUpgradeTargets(t, srv, pb.OperationKind_OPERATION_KIND_TALOS)

	if m.GetAvailable() || !strings.Contains(m.GetUnavailableReason(), "could not list the releases") {
		t.Fatalf("available = %v, reason = %q", m.GetAvailable(), m.GetUnavailableReason())
	}

	if m.GetDefault() != "v1.12.4" {
		t.Fatalf("the last good list should still be offered: default = %q", m.GetDefault())
	}
}

func TestListUpgradeTargetsRefusesAnotherKind(t *testing.T) {
	srv, _ := opServer(t, upgradeCluster("v1.14.3", "v1.34.2", "1.20.0"))

	_, err := srv.ListUpgradeTargets(context.Background(), connect.NewRequest(&pb.ListUpgradeTargetsRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_CNI,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestClusterRolloutRPCsAreUnimplemented(t *testing.T) {
	srv, _ := opServer(t, upgradeCluster("v1.14.3", "v1.34.2", "1.20.0"))

	_, err := srv.PlanClusterUpgrade(context.Background(), connect.NewRequest(&pb.PlanClusterUpgradeRequest{
		Cluster: objectRef(opNS, opCluster),
	}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("PlanClusterUpgrade err = %v, want Unimplemented", err)
	}
}
