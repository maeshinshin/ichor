package uiapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	cniVerifiedPatch   = "v1.20.6"
	cniUnverifiedPatch = "v1.20.7"
)

// cniOverrideServer is a cluster on Cilium 1.20.4 with Kubernetes 1.31, which Cilium 1.20 is not tested with, and an
// Ichor that verified only v1.20.6. allowed is the answer to the override access review.
func cniOverrideServer(t *testing.T, allowed bool) (*Server, client.Client, *[]authv1.ResourceAttributes) {
	t.Helper()

	cluster := upgradeCluster(fixtureTalos, "1.20.4")
	cluster.Spec.KubernetesVersion = "v1.31.0"

	inst := recordedLists(nil, nil, "")
	inst.Status.UpgradeTargets.Cilium = []string{cniVerifiedPatch}

	srv, k8s := opServer(t, cluster, inst)

	asked := new([]authv1.ResourceAttributes)
	srv.Impersonate = func(Identity) (client.Client, error) { return reviewing(k8s, allowed, asked), nil }

	return srv, k8s, asked
}

func startCniPatch(srv *Server, version string, bg *pb.Override) error {
	ctx := context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser})
	_, err := srv.StartCniUpgrade(ctx, connect.NewRequest(&pb.StartCniUpgradeRequest{
		Cluster: objectRef(opNS, opCluster), Version: version, Override: bg,
	}))

	return err
}

func TestStartCniUpgradeRefusesAnIncompatiblePatchWithoutOverride(t *testing.T) {
	srv, k8s, _ := cniOverrideServer(t, true)

	err := startCniPatch(srv, cniVerifiedPatch, nil)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "override") {
		t.Fatalf("err = %v, want FailedPrecondition naming the override", err)
	}

	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Meta().Get(overridableKey) != "true" {
		t.Fatalf("the refusal must be marked overridable: %v", err)
	}

	if len(listOps(t, k8s)) != 0 {
		t.Fatal("a refused upgrade created an operation")
	}
}

func TestStartCniUpgradeOverridesAnIncompatiblePatchAndRecordsAnEvent(t *testing.T) {
	srv, k8s, asked := cniOverrideServer(t, true)

	if err := startCniPatch(srv, cniVerifiedPatch, &pb.Override{Reason: bgReason}); err != nil {
		t.Fatal(err)
	}

	ops := listOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.CNI == nil || ops[0].Spec.CNI.ImageVersion != cniVerifiedPatch {
		t.Fatalf("operations = %+v", ops)
	}

	if len(*asked) != 1 || (*asked)[0].Verb != OverrideVerb || (*asked)[0].Resource != "talosclusters" || (*asked)[0].Name != opCluster {
		t.Fatalf("access reviews = %+v, want the override verb on the TalosCluster", *asked)
	}

	var events corev1.EventList
	if err := k8s.List(context.Background(), &events); err != nil {
		t.Fatal(err)
	}

	if len(events.Items) != 1 || events.Items[0].Reason != "Override" || !strings.Contains(events.Items[0].Message, "overridden under override: "+bgReason) {
		t.Fatalf("events = %+v", events.Items)
	}
}

func TestStartCniUpgradeOverrideWithoutReasonSaysSo(t *testing.T) {
	srv, k8s, _ := cniOverrideServer(t, true)

	if err := startCniPatch(srv, cniVerifiedPatch, &pb.Override{}); err != nil {
		t.Fatal(err)
	}

	var events corev1.EventList
	if err := k8s.List(context.Background(), &events); err != nil {
		t.Fatal(err)
	}

	if len(events.Items) != 1 || !strings.Contains(events.Items[0].Message, noReasonGiven) {
		t.Fatalf("events = %+v", events.Items)
	}
}

func TestStartCniUpgradeRefusesTheOverrideWithoutTheVerb(t *testing.T) {
	srv, k8s, _ := cniOverrideServer(t, false)

	err := startCniPatch(srv, cniVerifiedPatch, &pb.Override{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}

	if len(listOps(t, k8s)) != 0 {
		t.Fatal("a refused override created an operation")
	}
}

func TestStartCniUpgradeNeverOverridesAnUnverifiedPatch(t *testing.T) {
	srv, k8s, asked := cniOverrideServer(t, true)

	err := startCniPatch(srv, cniUnverifiedPatch, &pb.Override{Reason: bgReason})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("err = %v, want the not-offered refusal", err)
	}

	var ce *connect.Error
	if errors.As(err, &ce) && ce.Meta().Get(overridableKey) != "" {
		t.Fatal("an unverified patch must not be marked overridable")
	}

	if len(listOps(t, k8s)) != 0 || len(*asked) != 0 {
		t.Fatalf("operations %d, access reviews %d: nothing may run", len(listOps(t, k8s)), len(*asked))
	}
}

func TestStartCniUpgradeNeedsNoOverrideForACompatiblePatch(t *testing.T) {
	srv, k8s, asked := cniOverrideServer(t, true)

	cluster := getCluster(t, k8s)
	cluster.Spec.KubernetesVersion = "v1.34.2"

	if err := k8s.Update(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}

	if err := startCniPatch(srv, cniVerifiedPatch, nil); err != nil {
		t.Fatal(err)
	}

	if len(listOps(t, k8s)) != 1 || len(*asked) != 0 {
		t.Fatalf("operations %d, access reviews %d", len(listOps(t, k8s)), len(*asked))
	}
}
