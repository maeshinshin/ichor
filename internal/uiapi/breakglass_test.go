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
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Break-glass tests (docs/adr/0064): the Kubernetes API is a fake whose
// SelfSubjectAccessReview answer the test chooses, standing in for RBAC.

const (
	bgReason = "node-2 is down, PR review would take an hour"
	bgUser   = "oidc:alice"
)

// reviewing wraps c so a SelfSubjectAccessReview is answered with allowed
// and recorded in asked; everything else goes through.
func reviewing(c client.Client, allowed bool, asked *[]authv1.ResourceAttributes) client.Client {
	return interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				*asked = append(*asked, *sar.Spec.ResourceAttributes)
				sar.Status.Allowed = allowed

				return nil
			}

			return c.Create(ctx, obj, opts...)
		},
	})
}

// asUser makes e.srv call Kubernetes as a user for whom the break-glass
// SelfSubjectAccessReview answers allowed, and returns a context carrying
// that identity plus the reviews the server asked.
func (e *prEnv) asUser(allowed bool) (context.Context, *[]authv1.ResourceAttributes) {
	asked := &[]authv1.ResourceAttributes{}

	e.srv.Impersonate = func(Identity) (client.Client, error) { return reviewing(e.k8s, allowed, asked), nil }

	return context.WithValue(context.Background(), identityKey{}, Identity{User: bgUser}), asked
}

func bgApprove(reason string) *connect.Request[pb.ApproveMachineRequest] {
	req := approveReq()
	req.Msg.BreakGlass = &pb.BreakGlass{Reason: reason}

	return req
}

func (e *prEnv) events(t *testing.T) []corev1.Event {
	t.Helper()

	var l corev1.EventList
	if err := e.k8s.List(context.Background(), &l, client.InNamespace(trackedTestNS)); err != nil {
		t.Fatal(err)
	}

	return l.Items
}

func TestBreakGlassWritesDirectlyAndOpensTheMatchingPR(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	ctx, asked := e.asUser(true)

	res, err := e.srv.ApproveMachine(ctx, bgApprove(bgReason))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if !e.machine(t).Spec.IsApproved() {
		t.Fatal("break-glass must write the CR now")
	}

	if res.Msg.GetPullRequestUrl() != prPRURL {
		t.Fatalf("break-glass must also open the PR, url = %q", res.Msg.GetPullRequestUrl())
	}

	a := res.Msg.GetAudit()
	if a.GetAction() != "approve-break-glass" || a.GetActor() != bgUser || a.GetReason() != bgReason {
		t.Fatalf("audit = %+v", a)
	}

	if len(*asked) != 1 || (*asked)[0].Verb != "break-glass" || (*asked)[0].Resource != "talosmachines" ||
		(*asked)[0].Group != "ichor.maesh.dev" || (*asked)[0].Name != "m1" || (*asked)[0].Namespace != trackedTestNS {
		t.Fatalf("access review = %+v", *asked)
	}

	body := e.host.body["body"]
	if !strings.Contains(body, bgReason) || !strings.Contains(body, bgUser) || !strings.Contains(strings.ToLower(e.host.body["title"]), "break-glass") {
		t.Fatalf("PR must carry who and why:\n%s\n%s", e.host.body["title"], body)
	}

	ev := e.events(t)
	if len(ev) != 1 || ev[0].Reason != "BreakGlass" || ev[0].Type != corev1.EventTypeWarning ||
		!strings.Contains(ev[0].Message, bgReason) || !strings.Contains(ev[0].Message, bgUser) ||
		ev[0].InvolvedObject.Name != "m1" || ev[0].InvolvedObject.Kind != "TalosMachine" {
		t.Fatalf("events = %+v", ev)
	}

	if !strings.Contains(res.Msg.GetNotice(), "selfHeal") {
		t.Fatalf("notice must warn about Argo CD selfHeal: %q", res.Msg.GetNotice())
	}
}

func TestBreakGlassWithoutPermissionIsRefusedAndWritesNothing(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	ctx, _ := e.asUser(false)

	_, err := e.srv.ApproveMachine(ctx, bgApprove(bgReason))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}

	if e.machine(t).Spec.IsApproved() || e.host.hits != 0 || len(e.events(t)) != 0 {
		t.Fatal("a refused break-glass must write nothing, open no PR, emit no Event")
	}
}

func TestBreakGlassWithoutASignedInUserIsRefused(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})

	_, err := e.srv.ApproveMachine(context.Background(), bgApprove(bgReason))
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "OIDC") {
		t.Fatalf("OIDC off must refuse break-glass and say why: %v", err)
	}

	if e.machine(t).Spec.IsApproved() {
		t.Fatal("nothing may be written")
	}
}

func TestBreakGlassRunsOnTheCallersOwnCredentialsInTheCLI(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	asked := &[]authv1.ResourceAttributes{}
	e.srv.Client = reviewing(e.k8s, true, asked)
	e.srv.CallerCredentials = true

	if _, err := e.srv.ApproveMachine(context.Background(), bgApprove(bgReason)); err != nil {
		t.Fatal(err)
	}

	if len(*asked) != 1 || !e.machine(t).Spec.IsApproved() {
		t.Fatalf("the caller's own RBAC must decide: asked=%v", *asked)
	}

	e = newPREnv(t, prOpts{tracked: true})
	e.srv.Client = reviewing(e.k8s, false, asked)
	e.srv.CallerCredentials = true

	_, err := e.srv.ApproveMachine(context.Background(), bgApprove(bgReason))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

func TestBreakGlassNeedsAReason(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	ctx, _ := e.asUser(true)

	for _, reason := range []string{"", "   "} {
		_, err := e.srv.ApproveMachine(ctx, bgApprove(reason))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("reason %q: want InvalidArgument, got %v", reason, err)
		}
	}
}

func TestBreakGlassOnAnUntrackedCRIsAnOrdinaryWrite(t *testing.T) {
	e := newPREnv(t, prOpts{})
	ctx, asked := e.asUser(true)

	res, err := e.srv.ApproveMachine(ctx, bgApprove(bgReason))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetAudit().GetAction() != "approve" || len(*asked) != 0 || e.host.hits != 0 || len(e.events(t)) != 0 {
		t.Fatal("when nothing stands in the way, break-glass is not used, checked or recorded")
	}
}

func TestBreakGlassNeedsARepositoryToOpenThePR(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true, noRepo: true})
	ctx, _ := e.asUser(true)

	_, err := e.srv.ApproveMachine(ctx, bgApprove(bgReason))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("a write Git cannot catch up to is refused: %v", err)
	}

	if e.machine(t).Spec.IsApproved() {
		t.Fatal("nothing may be written")
	}
}

func TestBreakGlassSaysSelfHealWillRevertWhenTheApplicationHasIt(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	ctx, _ := e.asUser(true)

	var app unstructured.Unstructured

	app.SetGroupVersionKind(argoApplicationGVK)

	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: trackedArgoNS, Name: "prod-root"}, &app); err != nil {
		t.Fatal(err)
	}

	_ = unstructured.SetNestedField(app.Object, true, "spec", "syncPolicy", "automated", "selfHeal")

	if err := e.k8s.Update(ctx, &app); err != nil {
		t.Fatal(err)
	}

	res, err := e.srv.ApproveMachine(ctx, bgApprove(bgReason))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"prod-root", "selfHeal", "reverted", "pull request"} {
		if !strings.Contains(res.Msg.GetNotice(), want) {
			t.Fatalf("notice %q lacks %q", res.Msg.GetNotice(), want)
		}
	}
}

func TestBreakGlassKeepsTheWriteWhenThePRFailsAndSaysSo(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true, hostStatus: http.StatusInternalServerError})
	ctx, _ := e.asUser(true)

	res, err := e.srv.ApproveMachine(ctx, bgApprove(bgReason))
	if err != nil {
		t.Fatalf("the write happened, so the call must not fail: %v", err)
	}

	if !e.machine(t).Spec.IsApproved() || res.Msg.GetPullRequestUrl() != "" {
		t.Fatal("the direct write stays and there is no PR URL")
	}

	if !strings.Contains(res.Msg.GetNotice(), "pull request could not be opened") || strings.Contains(res.Msg.GetNotice(), prToken) {
		t.Fatalf("notice = %q", res.Msg.GetNotice())
	}
}

func TestBreakGlassPatchUpdateAndDelete(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	ctx, asked := e.asUser(true)

	up, err := e.srv.UpdatePatch(ctx, connect.NewRequest(&pb.UpdatePatchRequest{
		Patch: sysctlPatchWire("p1", "c"), BreakGlass: &pb.BreakGlass{Reason: bgReason},
	}))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var p ichorv1alpha1.TalosConfigPatch
	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: trackedTestNS, Name: "p1"}, &p); err != nil || len(p.Spec.Documents) == 0 {
		t.Fatalf("the patch must be written: %v %+v", err, p.Spec)
	}

	if up.Msg.GetAudit().GetAction() != "update-break-glass" || up.Msg.GetPullRequestUrl() != prPRURL {
		t.Fatalf("update = %+v", up.Msg)
	}

	del, err := e.srv.DeletePatch(ctx, connect.NewRequest(&pb.DeletePatchRequest{
		Patch: objectRef(trackedTestNS, "p1"), BreakGlass: &pb.BreakGlass{Reason: bgReason},
	}))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := e.k8s.Get(ctx, types.NamespacedName{Namespace: trackedTestNS, Name: "p1"}, &p); err == nil {
		t.Fatal("the patch must be deleted")
	}

	if del.Msg.GetAudit().GetAction() != "delete-break-glass" || del.Msg.GetPullRequestUrl() == "" {
		t.Fatalf("delete = %+v", del.Msg)
	}

	if (*asked)[0].Resource != "talosconfigpatches" {
		t.Fatalf("access review = %+v", *asked)
	}
}

func TestBreakGlassRejectMachine(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	ctx, _ := e.asUser(true)

	res, err := e.srv.RejectMachine(ctx, connect.NewRequest(&pb.RejectMachineRequest{
		Machine: objectRef(trackedTestNS, "m1"), BreakGlass: &pb.BreakGlass{Reason: bgReason},
	}))
	if err != nil {
		t.Fatal(err)
	}

	if e.machine(t).Spec.ApprovalState != ichorv1alpha1.MachineApprovalStateRejected || res.Msg.GetAudit().GetAction() != "reject-break-glass" {
		t.Fatalf("reject = %+v", res.Msg)
	}
}
