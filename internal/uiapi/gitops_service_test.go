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
	"time"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	testAppName   = "prod-root"
	testOutOfSync = "OutOfSync"
	testDegraded  = "Degraded"
)

// Tests for the GitOps screen's backend: fake client, an
// httptest Git host, nothing real.

func setAppStatus(t *testing.T, e *prEnv, sync, health string) {
	t.Helper()

	var app unstructured.Unstructured

	app.SetGroupVersionKind(argoApplicationGVK)

	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: trackedArgoNS, Name: testAppName}, &app); err != nil {
		t.Fatal(err)
	}

	_ = unstructured.SetNestedField(app.Object, sync, "status", "sync", "status")
	_ = unstructured.SetNestedField(app.Object, health, "status", "health", "status")

	if err := e.k8s.Update(context.Background(), &app); err != nil {
		t.Fatal(err)
	}
}

func gitOpsItems(t *testing.T, e *prEnv) map[string]*pb.GitOpsItem {
	t.Helper()

	res, err := e.srv.ListGitOps(context.Background(), connect.NewRequest(&pb.ListGitOpsRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]*pb.GitOpsItem{}
	for _, it := range res.Msg.GetItems() {
		out[it.GetKind().String()+"/"+it.GetRef().GetName()] = it
	}

	return out
}

func TestListGitOpsReportsApplicationState(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	setAppStatus(t, e, testOutOfSync, testDegraded)

	items := gitOpsItems(t, e)

	m := items["GIT_OPS_KIND_MACHINE/m1"]
	if m.GetTrackedBy() != "Argo CD" || m.GetApplication().GetName() != testAppName ||
		m.GetSyncStatus() != testOutOfSync || m.GetHealthStatus() != testDegraded || m.GetError() != "" {
		t.Fatalf("machine row = %v", m)
	}

	if p := items["GIT_OPS_KIND_PATCH/p1"]; p.GetSyncStatus() != testOutOfSync {
		t.Fatalf("patch row = %v", p)
	}

	// The cluster carries no tracking marker: listed, but nothing to report.
	if c := items["GIT_OPS_KIND_CLUSTER/c"]; c == nil || c.GetTrackedBy() != "" || c.GetApplication() != nil {
		t.Fatalf("cluster row = %v", c)
	}
}

func TestListGitOpsMissingApplicationIsARowErrorNotAFailure(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})

	var app unstructured.Unstructured

	app.SetGroupVersionKind(argoApplicationGVK)
	app.SetName(testAppName)
	app.SetNamespace(trackedArgoNS)

	if err := e.k8s.Delete(context.Background(), &app); err != nil {
		t.Fatal(err)
	}

	m := gitOpsItems(t, e)["GIT_OPS_KIND_MACHINE/m1"]
	if m.GetError() == "" || m.GetSyncStatus() != "" || m.GetApplication().GetName() != testAppName {
		t.Fatalf("machine row = %v", m)
	}
}

func findPRsReq(kind pb.GitOpsKind, name string) *connect.Request[pb.FindPullRequestsRequest] {
	return connect.NewRequest(&pb.FindPullRequestsRequest{Kind: kind, Ref: objectRef(trackedTestNS, name)})
}

func TestFindPullRequestsAsksTheHostOnceWithinTheTTL(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	e.host.open[prBranchName("approve", trackedTestNS, "m1")] = prPRURL

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	e.srv.Now = func() time.Time { return now }

	ask := func() *pb.FindPullRequestsResponse {
		res, err := e.srv.FindPullRequests(context.Background(), findPRsReq(pb.GitOpsKind_GIT_OPS_KIND_MACHINE, "m1"))
		if err != nil {
			t.Fatal(err)
		}

		return res.Msg
	}

	got := ask()
	if len(got.GetPullRequests()) != 1 || got.GetPullRequests()[0].GetAction() != "approve" || got.GetPullRequests()[0].GetUrl() != prPRURL {
		t.Fatalf("pull requests = %v", got.GetPullRequests())
	}

	if e.host.gets != 2 { // approve and reject
		t.Fatalf("first ask: host lookups = %d, want 2", e.host.gets)
	}

	now = now.Add(10 * time.Second)
	ask()

	if e.host.gets != 2 {
		t.Fatalf("within the TTL the host was asked again: %d lookups", e.host.gets)
	}

	now = now.Add(prLookupTTL)
	ask()

	if e.host.gets != 4 {
		t.Fatalf("after the TTL the host must be asked again: %d lookups", e.host.gets)
	}
}

func TestFindPullRequestsForADirectlyWrittenCRSkipsTheHost(t *testing.T) {
	e := newPREnv(t, prOpts{noRepo: true})

	res, err := e.srv.FindPullRequests(context.Background(), findPRsReq(pb.GitOpsKind_GIT_OPS_KIND_PATCH, "p1"))
	if err != nil {
		t.Fatal(err)
	}

	if res.Msg.GetNote() == "" || len(res.Msg.GetPullRequests()) != 0 || e.host.gets != 0 {
		t.Fatalf("note=%q prs=%v lookups=%d", res.Msg.GetNote(), res.Msg.GetPullRequests(), e.host.gets)
	}
}

func TestFindPullRequestsHostErrorIsNotCachedAndLeaksNoToken(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})
	e.host.getStatus = http.StatusForbidden

	_, err := e.srv.FindPullRequests(context.Background(), findPRsReq(pb.GitOpsKind_GIT_OPS_KIND_MACHINE, "m1"))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("want Unavailable, got %v", err)
	}

	if strings.Contains(err.Error(), prToken) {
		t.Fatalf("the token leaked: %v", err)
	}

	// The host recovers: the failure must not have been remembered as "none".
	e.host.getStatus = 0
	e.host.open[prBranchName("approve", trackedTestNS, "m1")] = prPRURL

	res, err := e.srv.FindPullRequests(context.Background(), findPRsReq(pb.GitOpsKind_GIT_OPS_KIND_MACHINE, "m1"))
	if err != nil || len(res.Msg.GetPullRequests()) != 1 {
		t.Fatalf("err=%v res=%v", err, res)
	}
}

func TestGetLinksDropsAnythingButPlainHTTP(t *testing.T) {
	s := &Server{Links: Links{
		ArgoCD:  "https://argocd.example.com",
		Grafana: "javascript:alert(1)",
		Hubble:  "https://user:pw@hubble.example.com",
		// Longhorn is not registered.
	}}

	res, err := s.GetLinks(context.Background(), connect.NewRequest(&pb.GetLinksRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	m := res.Msg
	if m.GetArgocd() != "https://argocd.example.com" || m.GetGrafana() != "" || m.GetHubble() != "" || m.GetLonghorn() != "" {
		t.Fatalf("links = %v", m)
	}
}
