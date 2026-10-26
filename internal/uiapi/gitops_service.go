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
	"fmt"
	"net/url"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
	"github.com/maeshinshin/ichor/pkg/prmode"
)

// prLookupTTL is how long an answer from a Git host is reused.
// Short: the screen is for "is my change waiting for review", and a PR merged
// a minute ago still showing as open is the worst it can say.
const prLookupTTL = 30 * time.Second

// prLookup is one cached answer, keyed by host, repository and branches.
type prLookup struct {
	at  time.Time
	url string // empty: no open pull request
}

// prCache is zero-value usable. It is per replica; two replicas may each ask
// the host once per TTL.
type prCache struct {
	mu sync.Mutex
	m  map[string]prLookup
}

func (c *prCache) get(key string, now time.Time) (prLookup, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	l, ok := c.m[key]
	if !ok || now.Sub(l.at) >= prLookupTTL {
		return prLookup{}, false
	}

	return l, true
}

func (c *prCache) put(key string, l prLookup) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == nil {
		c.m = map[string]prLookup{}
	}

	c.m[key] = l
}

// Links are the external tool base URLs from the chart's values.
type Links struct {
	ArgoCD, Grafana, Hubble, Longhorn string
}

// SanitizeLink returns raw if it is an absolute http(s) URL without
// credentials, else "". A registered URL is relayed to every browser, so
// anything else (a javascript: URL, a user:password@ URL) is dropped here as
// well as in the UI.
func SanitizeLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}

	return u.String()
}

// ListGitOps reads each CR's tracking Application. The CR lists and the
// Application get both use the caller's identity: a caller who
// may not read the Application gets a permission_denied row, not its sync and
// health. Which tracker owns a CR is decided with the ServiceAccount, but that
// answer is about the CR the caller already listed.
func (s *Server) ListGitOps(
	ctx context.Context, _ *connect.Request[pb.ListGitOpsRequest],
) (*connect.Response[pb.ListGitOpsResponse], error) {
	var (
		clusters ichorv1alpha1.TalosClusterList
		machines ichorv1alpha1.TalosMachineList
		patches  ichorv1alpha1.TalosConfigPatchList
	)

	for _, l := range []client.ObjectList{&clusters, &machines, &patches} {
		if err := s.List(ctx, l); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	var items []*pb.GitOpsItem

	apps := map[string]*pb.GitOpsItem{} // one Application read per Application

	add := func(kind pb.GitOpsKind, obj client.Object, cluster string) {
		items = append(items, s.gitOpsItem(ctx, kind, obj, cluster, apps))
	}

	for i := range clusters.Items {
		add(pb.GitOpsKind_GIT_OPS_KIND_CLUSTER, &clusters.Items[i], clusters.Items[i].Name)
	}

	for i := range machines.Items {
		add(pb.GitOpsKind_GIT_OPS_KIND_MACHINE, &machines.Items[i], machines.Items[i].Spec.ClusterRef.Name)
	}

	for i := range patches.Items {
		add(pb.GitOpsKind_GIT_OPS_KIND_PATCH, &patches.Items[i], patches.Items[i].Spec.ClusterRef.Name)
	}

	return connect.NewResponse(&pb.ListGitOpsResponse{Items: items}), nil
}

func (s *Server) gitOpsItem(
	ctx context.Context, kind pb.GitOpsKind, obj client.Object, cluster string, apps map[string]*pb.GitOpsItem,
) *pb.GitOpsItem {
	item := &pb.GitOpsItem{Kind: kind, Ref: objectRef(obj.GetNamespace(), obj.GetName()), Cluster: cluster}

	tracked, by, err := gitopstracking.IsTracked(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		item.Error = "cannot tell whether Argo CD or Flux tracks this: " + err.Error()

		return item
	}

	item.TrackedBy = by
	if !tracked || by != gitopstracking.TrackedByArgoCD {
		return item
	}

	appKey, err := gitopstracking.ArgoApplicationOf(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		item.Error = "the tracking Application cannot be named: " + err.Error()

		return item
	}

	item.Application = objectRef(appKey.Namespace, appKey.Name)

	if seen, ok := apps[appKey.String()]; ok {
		item.SyncStatus, item.HealthStatus, item.Error, item.PermissionDenied = seen.SyncStatus, seen.HealthStatus, seen.Error, seen.PermissionDenied

		return item
	}

	var app unstructured.Unstructured

	app.SetGroupVersionKind(argoApplicationGVK)

	// Read as the caller: the status words go back to them.
	// Without OIDC there is no identity and s.Get is the ServiceAccount.
	switch err := s.Get(ctx, appKey, &app); {
	case apierrors.IsForbidden(err):
		item.PermissionDenied = true
	case err != nil:
		item.Error = fmt.Sprintf("Application %s cannot be read: %v", appKey, err)
	default:
		item.SyncStatus, _, _ = unstructured.NestedString(app.Object, "status", "sync", "status")
		item.HealthStatus, _, _ = unstructured.NestedString(app.Object, "status", "health", "status")
	}

	apps[appKey.String()] = item

	return item
}

// prVerbs are the changes PR mode proposes for each kind (gitops_pr_ops.go),
// which is also the set of head branches ichor/<verb>-<name> to look for.
const (
	verbApprove = "approve"
	verbReject  = "reject"
	verbUpdate  = "update"
	verbDelete  = "delete"
)

func prVerbs(kind pb.GitOpsKind) []string {
	switch kind {
	case pb.GitOpsKind_GIT_OPS_KIND_MACHINE:
		return []string{verbApprove, verbReject}
	case pb.GitOpsKind_GIT_OPS_KIND_PATCH:
		return []string{verbUpdate, verbDelete}
	default:
		return nil
	}
}

// FindPullRequests lists the open pull requests of one CR: one host call per
// verb the kind can propose, each answered from the cache for prLookupTTL. The
// CR itself is read as the caller first, so asking about a CR the caller cannot
// see is refused rather than answered with the ServiceAccount's token.
func (s *Server) FindPullRequests(
	ctx context.Context, req *connect.Request[pb.FindPullRequestsRequest],
) (*connect.Response[pb.FindPullRequestsResponse], error) {
	var (
		obj     client.Object
		cluster string
	)

	key := namespacedName(req.Msg.GetRef())

	switch req.Msg.GetKind() {
	case pb.GitOpsKind_GIT_OPS_KIND_MACHINE:
		var m ichorv1alpha1.TalosMachine
		if err := s.Get(ctx, key, &m); err != nil {
			return nil, mapGetError("machine "+key.String(), err)
		}

		obj, cluster = &m, m.Spec.ClusterRef.Name
	case pb.GitOpsKind_GIT_OPS_KIND_PATCH:
		var p ichorv1alpha1.TalosConfigPatch
		if err := s.Get(ctx, key, &p); err != nil {
			return nil, mapGetError("patch "+key.String(), err)
		}

		obj, cluster = &p, p.Spec.ClusterRef.Name
	case pb.GitOpsKind_GIT_OPS_KIND_UNSPECIFIED, pb.GitOpsKind_GIT_OPS_KIND_CLUSTER:
		return connect.NewResponse(&pb.FindPullRequestsResponse{Note: "Ichor opens no pull requests for this kind"}), nil
	}

	if err := s.requireApplicationRead(ctx, obj); err != nil {
		return nil, err
	}

	plan, err := s.planWrite(ctx, obj, cluster, req.Msg.GetKind() == pb.GitOpsKind_GIT_OPS_KIND_MACHINE, nil)
	if err != nil {
		// A refusal (read-only because of Flux, say) is the answer, not a failure.
		return connect.NewResponse(&pb.FindPullRequestsResponse{Note: "no pull request applies: " + err.Error()}), nil
	}

	if plan == nil {
		return connect.NewResponse(&pb.FindPullRequestsResponse{
			Note: "this resource is written directly, so it never goes through a pull request",
		}), nil
	}

	return s.findPRs(ctx, plan, req.Msg.GetKind(), obj.GetName())
}

// requireApplicationRead refuses a caller who may not read the Argo CD
// Application tracking obj. The pull requests are looked up
// with the repository token, which is the ServiceAccount's side; asking them
// of a caller who may not see the Application would show GitOps state they
// were refused on the page. An untracked or Flux-tracked CR has no
// Application, so there is nothing to check.
func (s *Server) requireApplicationRead(ctx context.Context, obj client.Object) error {
	_, by, err := gitopstracking.IsTracked(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}

	if by != gitopstracking.TrackedByArgoCD {
		return nil
	}

	appKey, err := gitopstracking.ArgoApplicationOf(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		return nil //nolint:nilerr // planWrite refuses with the reason
	}

	var app unstructured.Unstructured

	app.SetGroupVersionKind(argoApplicationGVK)

	if err := s.Get(ctx, appKey, &app); apierrors.IsForbidden(err) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not read the Argo CD Application %s that tracks this resource", appKey))
	}

	return nil
}

func (s *Server) findPRs(
	ctx context.Context, plan *prPlan, kind pb.GitOpsKind, name string,
) (*connect.Response[pb.FindPullRequestsResponse], error) {
	token, err := s.gitToken(ctx, plan.repo)
	if err != nil {
		return nil, err
	}

	res := &pb.FindPullRequestsResponse{}
	oldest := s.now()

	for _, verb := range prVerbs(kind) {
		head := fmt.Sprintf("ichor/%s-%s", verb, name)
		key := plan.repo.Host + "|" + plan.repo.APIURL + "|" + plan.repo.Owner + "/" + plan.repo.Repo + "|" + head + "|" + plan.baseBranch

		l, ok := s.prs.get(key, s.now())
		if !ok {
			found, has, err := prmode.FindOpenPullRequest(ctx, hostOf(plan.repo.Host), prmode.PRRequest{
				APIBaseURL: plan.repo.APIURL, Owner: plan.repo.Owner, Repo: plan.repo.Repo, Token: token,
				BaseBranch: plan.baseBranch, HeadBranch: head,
			})
			if err != nil {
				// An error is never cached and never "none".
				return nil, connect.NewError(connect.CodeUnavailable,
					fmt.Errorf("asking the Git host for open pull requests failed: %s", redact(err.Error(), token)))
			}

			l = prLookup{at: s.now()}
			if has {
				l.url = found.URL
			}

			s.prs.put(key, l)
		}

		if l.at.Before(oldest) {
			oldest = l.at
		}

		if l.url != "" {
			res.PullRequests = append(res.PullRequests, &pb.PullRequest{Action: verb, Url: l.url})
		}
	}

	res.CheckedAt = timestamppb.New(oldest)

	return connect.NewResponse(res), nil
}

// GetLinks returns the registered external tool URLs.
func (s *Server) GetLinks(
	_ context.Context, _ *connect.Request[pb.GetLinksRequest],
) (*connect.Response[pb.GetLinksResponse], error) {
	return connect.NewResponse(&pb.GetLinksResponse{
		Argocd: SanitizeLink(s.Links.ArgoCD), Grafana: SanitizeLink(s.Links.Grafana),
		Hubble: SanitizeLink(s.Links.Hubble), Longhorn: SanitizeLink(s.Links.Longhorn),
	}), nil
}
