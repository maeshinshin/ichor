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
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Tests for PR mode wiring: the PR host is an httptest
// server, git talks to a bare repository in a temp directory, and nothing
// reaches a real host.

const (
	prToken     = "tok-SECRET-0123456789"
	prBackendNS = "ichor-system"
	prPRURL     = "https://git.invalid/acme/infra/pulls/3"
	prAppPath   = "clusters/prod"
)

const prSeedFile = `# the nodes
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosMachine
metadata:
  name: m1
  namespace: default
spec:
  clusterRef:
    name: c # the cluster
  identity:
    uuid: u
  endpoint: 10.0.0.10:50000
  approvalState: pending
---
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosConfigPatch
metadata:
  name: p1
  namespace: default
spec:
  clusterRef:
    name: c
  layer: cluster
  priority: 10
  documents: []
`

type prHost struct {
	srv  *httptest.Server
	body map[string]string // the last PR request body
	auth string
	hits int
}

// newPRHost serves "open a PR" as Gitea does. status != 201 makes it fail
// and echo the Authorization header back, the worst case for a leak.
func newPRHost(t *testing.T, status int) *prHost {
	t.Helper()

	h := &prHost{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits++
		h.auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &h.body)

		if status != http.StatusCreated {
			http.Error(w, "denied for "+h.auth, status)

			return
		}

		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": prPRURL})
	}))
	t.Cleanup(h.srv.Close)

	return h
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func newBare(t *testing.T) string {
	t.Helper()

	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}

	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}

	bare := filepath.Join(t.TempDir(), "bare.git")
	work := filepath.Join(t.TempDir(), "seed")

	git(t, "", "init", "--bare", "-b", "main", bare)
	git(t, "", "init", "-b", "main", work)

	if err := os.MkdirAll(filepath.Join(work, prAppPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(work, prAppPath, "nodes.yaml"), []byte(prSeedFile), 0o644); err != nil {
		t.Fatal(err)
	}

	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "seed")
	git(t, work, "remote", "add", "origin", bare)
	git(t, work, "push", "origin", "main")

	return bare
}

// fileOnBranch returns path's content on the pushed head branch of bare.
func fileOnBranch(t *testing.T, bare, path string) string {
	t.Helper()

	out, err := exec.Command("git", "-C", bare, "for-each-ref", "--format=%(refname:short)", "refs/heads/ichor/").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("no ichor/ branch was pushed: %v %s", err, out)
	}

	branch := strings.Fields(string(out))[0]

	show, err := exec.Command("git", "-C", bare, "show", branch+":"+path).Output()
	if err != nil {
		t.Fatalf("git show %s:%s: %v", branch, path, err)
	}

	return string(show)
}

func noBranch(t *testing.T, bare string) {
	t.Helper()

	out, _ := exec.Command("git", "-C", bare, "for-each-ref", "refs/heads/ichor/").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a branch was pushed: %s", out)
	}
}

type prEnv struct {
	k8s  client.Client
	srv  *Server
	bare string
	host *prHost
}

type prOpts struct {
	strict     bool
	noRepo     bool
	tracked    bool // machine and patch carry an Argo CD tracking annotation
	appRepoURL string
	hostStatus int
	secretNS   string
}

func newPREnv(t *testing.T, o prOpts) *prEnv {
	t.Helper()

	if o.hostStatus == 0 {
		o.hostStatus = http.StatusCreated
	}

	if o.secretNS == "" {
		o.secretNS = prBackendNS
	}

	bare := newBare(t)
	host := newPRHost(t, o.hostStatus)

	cluster := &ichorv1alpha1.TalosCluster{Name: "c", Namespace: trackedTestNS}
	cluster.Spec.VersionContract = testVersionContract
	if !o.noRepo {
		cluster.Spec.GitOps = &ichorv1alpha1.TalosClusterGitOps{
			Mode: "relaxed",
			Repository: &ichorv1alpha1.TalosClusterGitRepository{
				Host: "gitea", APIURL: host.srv.URL, Owner: "acme", Repo: "infra", CloneURL: bare, BaseBranch: "main",
				MachinesPath: prAppPath + "/machines",
				TokenSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "git-token"}, Key: "token",
				},
			},
		}
		if o.strict {
			cluster.Spec.GitOps.Mode = "strict"
		}
	}

	var annotations map[string]string
	if o.tracked {
		annotations = map[string]string{argoTrackingAnnot: "prod-root:ichor.maesh.dev/TalosMachine:default/m1"}
	}

	m := trackedMachine(annotations)
	p := trackedPatch()
	p.Annotations = annotations
	p.Spec = ichorv1alpha1.TalosConfigPatchSpec{
		ClusterRef: corev1.LocalObjectReference{Name: "c"}, Layer: "cluster", Priority: 10,
	}

	if o.appRepoURL == "" {
		o.appRepoURL = bare
	}

	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: ichorv1alpha1.GroupVersion.Version, Kind: "Application"})
	app.SetName("prod-root")
	app.SetNamespace(trackedArgoNS)
	_ = unstructured.SetNestedField(app.Object, o.appRepoURL, "spec", "source", "repoURL")
	_ = unstructured.SetNestedField(app.Object, prAppPath, "spec", "source", "path")
	_ = unstructured.SetNestedField(app.Object, "main", "spec", "source", "targetRevision")

	secret := &corev1.Secret{Name: "git-token", Namespace: o.secretNS, Data: map[string][]byte{"token": []byte(prToken + "\n")}}

	k8s := fake.NewClientBuilder().WithScheme(trackedScheme(t)).WithObjects(cluster, m, p, argoCM(), app, secret).Build()

	return &prEnv{k8s: k8s, bare: bare, host: host, srv: &Server{
		Client: k8s, ControllerNamespace: prBackendNS, ArgoCDNamespace: trackedArgoNS,
	}}
}

func (e *prEnv) machine(t *testing.T) ichorv1alpha1.TalosMachine {
	t.Helper()

	var m ichorv1alpha1.TalosMachine
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "m1"}, &m); err != nil {
		t.Fatal(err)
	}

	return m
}

func TestStrictApprovalOpensAPRThatAddsTheMachine(t *testing.T) {
	e := newPREnv(t, prOpts{strict: true})

	res, err := e.srv.ApproveMachine(context.Background(), approveReq())
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if res.Msg.GetPullRequestUrl() != prPRURL || res.Msg.GetAudit().GetAction() != "approve-pr" {
		t.Fatalf("url=%q action=%q", res.Msg.GetPullRequestUrl(), res.Msg.GetAudit().GetAction())
	}

	if e.machine(t).Spec.IsApproved() || res.Msg.GetMachine().GetApprovalState() == pb.MachineApprovalState_MACHINE_APPROVAL_STATE_APPROVED {
		t.Fatal("a strict approval must not write the TalosMachine; it stays pending")
	}

	added := fileOnBranch(t, e.bare, prAppPath+"/machines/m1.yaml")
	for _, want := range []string{"kind: TalosMachine", "name: m1", "approvalState: approved", "role: worker"} {
		if !strings.Contains(added, want) {
			t.Fatalf("added file lacks %q:\n%s", want, added)
		}
	}

	if !strings.Contains(e.host.body["body"], "## Impact") || !strings.Contains(e.host.body["body"], "Requested by: ichor-ui") {
		t.Fatalf("PR description lacks impact or requester:\n%s", e.host.body["body"])
	}

	if e.host.body["base"] != "main" || e.host.body["head"] == "" {
		t.Fatalf("PR branches = %v", e.host.body)
	}
}

func TestStrictApprovalStillChecksPreconditions(t *testing.T) {
	e := newPREnv(t, prOpts{strict: true})

	_, err := e.srv.ApproveMachine(context.Background(), connect.NewRequest(&pb.ApproveMachineRequest{
		Machine: objectRef(trackedTestNS, "m1"),
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a missing role must be refused before any PR: %v", err)
	}

	if e.host.hits != 0 {
		t.Fatal("the PR host must not be called")
	}
}

func TestRelaxedApprovalStillWritesDirectly(t *testing.T) {
	e := newPREnv(t, prOpts{})

	if _, err := e.srv.ApproveMachine(context.Background(), approveReq()); err != nil {
		t.Fatal(err)
	}

	if !e.machine(t).Spec.IsApproved() || e.host.hits != 0 {
		t.Fatal("relaxed + untracked must write the CR and open no PR")
	}
}

func TestTrackedMachineApprovalEditsTheFileInPlace(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})

	res, err := e.srv.ApproveMachine(context.Background(), approveReq())
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if res.Msg.GetPullRequestUrl() != prPRURL {
		t.Fatalf("url = %q", res.Msg.GetPullRequestUrl())
	}

	if e.machine(t).Spec.IsApproved() {
		t.Fatal("a tracked CR must not be written")
	}

	edited := fileOnBranch(t, e.bare, prAppPath+"/nodes.yaml")
	for _, want := range []string{"# the nodes", "# the cluster", "approvalState: approved", "role: worker", "kind: TalosConfigPatch"} {
		if !strings.Contains(edited, want) {
			t.Fatalf("edited file lacks %q:\n%s", want, edited)
		}
	}
}

func TestTrackedPatchUpdateOpensAPR(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})

	res, err := e.srv.UpdatePatch(context.Background(), connect.NewRequest(&pb.UpdatePatchRequest{
		Patch: sysctlPatchWire("p1", "c"),
	}))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if res.Msg.GetPullRequestUrl() != prPRURL || res.Msg.GetAudit().GetAction() != "update-pr" {
		t.Fatalf("url=%q action=%q", res.Msg.GetPullRequestUrl(), res.Msg.GetAudit().GetAction())
	}

	if !strings.Contains(e.host.body["body"], "Layer: ") {
		t.Fatalf("PR description lacks the impact:\n%s", e.host.body["body"])
	}

	if edited := fileOnBranch(t, e.bare, prAppPath+"/nodes.yaml"); !strings.Contains(edited, "sysctl") && !strings.Contains(edited, "Sysctl") {
		t.Fatalf("the patch documents were not written:\n%s", edited)
	}

	var p ichorv1alpha1.TalosConfigPatch
	if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "p1"}, &p); err != nil || len(p.Spec.Documents) != 0 {
		t.Fatalf("the cluster's patch must be untouched: %v %+v", err, p.Spec)
	}
}

func TestTrackedWithoutConnectionIsStillRefused(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true, noRepo: true})

	_, err := e.srv.ApproveMachine(context.Background(), approveReq())
	wantRefused(t, err, "Argo CD")

	if e.host.hits != 0 {
		t.Fatal("no PR without a connection")
	}
}

func TestTrackedPatchDeleteIsRefusedEvenWithAConnection(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true})

	_, err := e.srv.DeletePatch(context.Background(), connect.NewRequest(&pb.DeletePatchRequest{Patch: objectRef(trackedTestNS, "p1")}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}

	if e.host.hits != 0 {
		t.Fatal("no PR for a delete")
	}
}

func TestApplicationFromAnotherRepositoryIsRefused(t *testing.T) {
	e := newPREnv(t, prOpts{tracked: true, appRepoURL: "https://elsewhere.invalid/other/repo.git"})

	_, err := e.srv.ApproveMachine(context.Background(), approveReq())
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}

	noBranch(t, e.bare)
}

func TestTokenSecretIsReadInTheBackendNamespaceOnly(t *testing.T) {
	// The Secret exists only in the TalosCluster's namespace: RBAC does not
	// allow reading it there, so this must fail, not fall back.
	e := newPREnv(t, prOpts{strict: true, secretNS: trackedTestNS})

	_, err := e.srv.ApproveMachine(context.Background(), approveReq())
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "git-token") {
		t.Fatalf("want a FailedPrecondition naming the Secret, got %v", err)
	}

	if e.host.hits != 0 {
		t.Fatal("no PR without a token")
	}
}

// TestTokenNeverLeaks drives the failure paths a token could escape through
// -- the PR host rejecting the call and echoing the header, a clone that
// fails, an unreadable Secret key -- and checks the error, the PR body and
// the response for the token and its base64 header form.
func TestTokenNeverLeaks(t *testing.T) {
	check := func(t *testing.T, what string, parts ...string) {
		t.Helper()

		for _, p := range parts {
			if strings.Contains(p, prToken) || strings.Contains(p, "tok-SECRET") {
				t.Fatalf("%s leaked the token: %s", what, p)
			}
		}
	}

	t.Run("host rejects and echoes the Authorization header", func(t *testing.T) {
		e := newPREnv(t, prOpts{strict: true, hostStatus: http.StatusUnauthorized})

		_, err := e.srv.ApproveMachine(context.Background(), approveReq())
		if err == nil {
			t.Fatal("want an error")
		}

		check(t, "error", err.Error())
		check(t, "PR body", e.host.body["body"], e.host.body["title"])
	})

	t.Run("clone fails", func(t *testing.T) {
		e := newPREnv(t, prOpts{strict: true})
		e.srv.Client = e.k8s

		var c ichorv1alpha1.TalosCluster
		if err := e.k8s.Get(context.Background(), types.NamespacedName{Namespace: trackedTestNS, Name: "c"}, &c); err != nil {
			t.Fatal(err)
		}

		c.Spec.GitOps.Repository.CloneURL = filepath.Join(t.TempDir(), "missing.git")

		if err := e.k8s.Update(context.Background(), &c); err != nil {
			t.Fatal(err)
		}

		_, err := e.srv.ApproveMachine(context.Background(), approveReq())
		if err == nil {
			t.Fatal("want an error")
		}

		check(t, "error", err.Error())
	})

	t.Run("success", func(t *testing.T) {
		e := newPREnv(t, prOpts{strict: true})

		res, err := e.srv.ApproveMachine(context.Background(), approveReq())
		if err != nil {
			t.Fatal(err)
		}

		check(t, "response", res.Msg.String(), e.host.body["body"], fileOnBranch(t, e.bare, prAppPath+"/machines/m1.yaml"))

		if e.host.hits != 1 {
			t.Fatalf("hits = %d", e.host.hits)
		}
	})

	t.Run("redact scrubs both forms", func(t *testing.T) {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + prToken))
		got := redact("a "+prToken+" b "+basic+" c", prToken)
		check(t, "redact", got)

		if !strings.Contains(got, "[redacted]") {
			t.Fatal("expected a redaction marker")
		}
	})
}
