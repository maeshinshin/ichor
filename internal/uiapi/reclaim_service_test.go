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

package uiapi

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

const testReclaimAddr = "10.0.0.5"

const reclaimMarker = "TALOSCONFIG-MARKER-9f3a"

// TestReclaimRefusedWithoutTheVerb: RBAC alone decides; there is no installation switch.
func TestReclaimRefusedWithoutTheVerb(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	s := &Server{Client: k8s, ControllerNamespace: testNS,
		Impersonate: func(Identity) (client.Client, error) { return reviewingSubresources(k8s), nil }}
	ctx := ContextWithIdentity(context.Background(), Identity{User: userAlice})

	if _, err := s.CreateReclaim(ctx, connect.NewRequest(&pb.CreateReclaimRequest{Address: testReclaimAddr, Talosconfig: []byte(reclaimMarker)})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.ConfirmReclaim(ctx, connect.NewRequest(&pb.ConfirmReclaimRequest{Uuid: "x"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("confirm: %v", err)
	}

	res, err := s.CanReclaim(ctx, connect.NewRequest(&pb.CanReclaimRequest{}))
	if err != nil || res.Msg.GetAllowed() {
		t.Fatalf("can: %v %v", res, err)
	}

	var secrets corev1.SecretList
	if err := s.Client.List(ctx, &secrets); err != nil || len(secrets.Items) != 0 {
		t.Fatalf("a Secret was written without the verb: %v %v", secrets.Items, err)
	}
}

func TestCreateReclaimStoresSecretAndRegistersAddress(t *testing.T) {
	cluster := discoveryCluster(nil)
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cluster).Build()
	s := &Server{Client: k8s, ControllerNamespace: testNS,
		Impersonate: func(Identity) (client.Client, error) {
			return reviewing(k8s, true, &[]authv1.ResourceAttributes{}), nil
		}}
	ctx := ContextWithIdentity(context.Background(), Identity{User: bgUser})

	res, err := s.CreateReclaim(ctx, connect.NewRequest(&pb.CreateReclaimRequest{
		Address: "10.0.0.5:50000", Talosconfig: []byte(reclaimMarker), Cluster: objectRef(testNS, "c"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(res.Msg.String(), reclaimMarker) {
		t.Fatal("the response carries the credential")
	}

	var mr ichorv1alpha1.MachineReclaim
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNS, Name: res.Msg.GetReclaim().GetRef().GetName()}, &mr); err != nil {
		t.Fatal(err)
	}

	if mr.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation] != bgUser ||
		mr.Spec.ClusterRef == nil || mr.Spec.ClusterRef.Name != "c" {
		t.Fatalf("request %+v", mr)
	}

	var sec corev1.Secret
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNS, Name: mr.Spec.CredentialSecretRef}, &sec); err != nil {
		t.Fatal(err)
	}

	if sec.Type != ichorv1alpha1.ReclaimCredentialSecretType || sec.Labels[ichorv1alpha1.ReclaimLabel] == "" ||
		string(sec.Data[ichorv1alpha1.ReclaimCredentialKey]) != reclaimMarker || len(sec.OwnerReferences) != 1 {
		t.Fatalf("secret %+v", sec)
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.Discovery == nil || len(got.Spec.Discovery.Addresses) != 1 || got.Spec.Discovery.Addresses[0] != testReclaimAddr {
		t.Fatalf("discovery %+v", got.Spec.Discovery)
	}
}

// The cluster's own credentials need the cluster and no talosconfig, and write no Secret.
func TestCreateReclaimWithClusterCredentialsWritesNoSecret(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(discoveryCluster(nil)).Build()
	s := &Server{Client: k8s, ControllerNamespace: testNS,
		Impersonate: func(Identity) (client.Client, error) {
			return reviewing(k8s, true, &[]authv1.ResourceAttributes{}), nil
		}}
	ctx := ContextWithIdentity(context.Background(), Identity{User: bgUser})

	for name, req := range map[string]*pb.CreateReclaimRequest{
		"no cluster":      {Address: testReclaimAddr, UseClusterCredentials: true},
		"and a paste too": {Address: testReclaimAddr, UseClusterCredentials: true, Cluster: objectRef(testNS, "c"), Talosconfig: []byte("x")},
	} {
		if _, err := s.CreateReclaim(ctx, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%s: %v", name, err)
		}
	}

	res, err := s.CreateReclaim(ctx, connect.NewRequest(&pb.CreateReclaimRequest{
		Address: testReclaimAddr, UseClusterCredentials: true, Cluster: objectRef(testNS, "c"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	var mr ichorv1alpha1.MachineReclaim
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNS, Name: res.Msg.GetReclaim().GetRef().GetName()}, &mr); err != nil {
		t.Fatal(err)
	}

	if !mr.Spec.ClusterCredentials || mr.Spec.CredentialSecretRef != "" || mr.Spec.ClusterRef == nil || mr.Spec.ClusterRef.Name != "c" {
		t.Fatalf("request %+v", mr.Spec)
	}

	var secrets corev1.SecretList
	if err := k8s.List(ctx, &secrets); err != nil || len(secrets.Items) != 0 {
		t.Fatalf("a Secret was written: %v %v", secrets.Items, err)
	}
}

func TestConfirmReclaimWritesOnlyConfirmFields(t *testing.T) {
	before := &ichorv1alpha1.MachineReclaim{
		Namespace: testNS, Name: "r",
		Spec: ichorv1alpha1.MachineReclaimSpec{
			Address: testReclaimAddr, CredentialSecretRef: "s", Force: true, ContextName: "ctx",
		},
	}
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(before.DeepCopy()).Build()
	s := &Server{Client: k8s, ControllerNamespace: testNS,
		Impersonate: func(Identity) (client.Client, error) {
			return reviewing(k8s, true, &[]authv1.ResourceAttributes{}), nil
		}}

	if _, err := s.ConfirmReclaim(ContextWithIdentity(context.Background(), Identity{User: bgUser}), connect.NewRequest(&pb.ConfirmReclaimRequest{
		Reclaim: objectRef(testNS, "r"), Uuid: " abc ", ClusterName: "old",
	})); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.MachineReclaim
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(before), &got); err != nil {
		t.Fatal(err)
	}

	want := before.Spec
	want.ConfirmUUID = "abc"
	want.ConfirmClusterName = "old"

	if got.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation] != bgUser {
		t.Fatalf("annotations %v", got.Annotations)
	}

	if got.Spec != want {
		t.Fatalf("spec changed beyond confirmUUID: %+v", got.Spec)
	}
}

func TestAccessLogDropsReclaimBodies(t *testing.T) {
	var buf bytes.Buffer

	logger := log.New(&buf, "", 0)
	s := &Server{Client: fake.NewClientBuilder().WithScheme(testScheme(t)).Build(), ControllerNamespace: testNS}
	opts := connect.WithInterceptors(AccessLogInterceptor(logger, true))

	mux := http.NewServeMux()
	mux.Handle(ichorv1alpha1connect.NewReclaimServiceHandler(s, opts))
	mux.Handle(ichorv1alpha1connect.NewMachineServiceHandler(s, opts))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	rc := ichorv1alpha1connect.NewReclaimServiceClient(ts.Client(), ts.URL)
	_, _ = rc.CreateReclaim(context.Background(), connect.NewRequest(&pb.CreateReclaimRequest{
		Address: testReclaimAddr, Talosconfig: []byte(reclaimMarker),
	}))

	// Control: another service's body is logged when body logging is on, so the check below can fail.
	mc := ichorv1alpha1connect.NewMachineServiceClient(ts.Client(), ts.URL)
	_, _ = mc.GetMachine(context.Background(), connect.NewRequest(&pb.GetMachineRequest{Machine: objectRef("ns", "visible-marker")}))

	out := buf.String()
	if strings.Contains(out, reclaimMarker) {
		t.Fatalf("talosconfig in the log: %s", out)
	}

	if !strings.Contains(out, "visible-marker") || !strings.Contains(out, "CreateReclaim") {
		t.Fatalf("access log lacks the expected lines: %s", out)
	}
}

func TestReclaimToPBCarriesTheInspectionWarnings(t *testing.T) {
	mr := &ichorv1alpha1.MachineReclaim{}
	mr.Status.Inspection = &ichorv1alpha1.MachineReclaimInspection{
		UUID: "u", Warnings: []string{"belongs to cluster x, which Ichor manages"},
	}

	got := reclaimToPB(mr).GetInspection().GetWarnings()
	if len(got) != 1 || got[0] != "belongs to cluster x, which Ichor manages" {
		t.Fatalf("warnings = %v", got)
	}
}
