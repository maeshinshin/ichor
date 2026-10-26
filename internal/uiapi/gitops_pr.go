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
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
	"github.com/maeshinshin/ichor/pkg/prmode"
)

// argoApplicationGVK is Argo CD's Application, read as unstructured because
// Ichor takes no Go dependency on a CRD it does not own.
// specKey and fieldRole are the CR keys PR mode edits.
const (
	specKey   = "spec"
	fieldRole = "role"
)

var argoApplicationGVK = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}

// prPlan is the answer planWrite gives when a write must not touch the
// cluster and becomes a pull request instead. A nil plan
// means "write directly, as always".
type prPlan struct {
	repo *ichorv1alpha1.TalosClusterGitRepository

	// add is a strict-mode approval: the CR is not in Git, so the PR adds a
	// file under repo.MachinesPath. Otherwise the PR edits the file the
	// tracking Application's source holds, under sourcePath.
	add        bool
	sourcePath string
	baseBranch string

	// app and selfHeal name the tracking Application and say whether it
	// self-heals (planEdit); bg is set when the caller was granted break-glass: the CR is written directly and this PR
	// follows.
	app      string
	selfHeal bool
	bg       *breakGlass
}

// prChange is one proposed change: what to edit or add, and the words the PR
// carries.
type prChange struct {
	verb    string // "approve", "reject", "update": branch name and audit action
	name    string
	target  prmode.Target
	updates []prmode.FieldUpdate
	newFile *prmode.NewFile
	remove  bool // delete the CR's document (or its file)
	title   string
	impact  string
}

func (s *Server) argoNamespace() string {
	if s.ArgoCDNamespace != "" {
		return s.ArgoCDNamespace
	}

	return defaultArgoCDNamespace
}

// planWrite is the one gate every write to an existing CR passes through. It returns nil when the write may go
// straight to
// the cluster, a plan when it must become a pull request, and an error when
// it must not happen at all. A failed check refuses: "cannot tell" is never
// "may write".
//
// bg asks for break-glass: where the answer would be a pull
// request, the returned plan has bg set and the caller writes the CR directly
// and then opens that pull request. Where the write is direct anyway, bg is
// ignored; where no pull request can be opened, it is refused.
//
// allowStrictAdd is true only for approving a machine: under spec.gitops.mode
// strict, that is "a PR that adds the TalosMachine" even though nothing
// tracks the pending CR yet.
func (s *Server) planWrite(
	ctx context.Context, obj client.Object, clusterName string, allowStrictAdd bool, bg *pb.BreakGlass,
) (*prPlan, error) {
	key := client.ObjectKeyFromObject(obj)

	_, by, err := gitopstracking.IsTracked(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("cannot tell whether Argo CD or Flux tracks %q, so it is not written: %w", key, err))
	}

	repo, strict, err := s.gitOpsOf(ctx, obj.GetNamespace(), clusterName)
	if err != nil {
		return nil, err
	}

	switch prmode.Decide(by, repo != nil) {
	case prmode.ModeDirectWrite:
		if !strict || !allowStrictAdd {
			return nil, nil
		}

		if repo == nil || repo.MachinesPath == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("cluster %q is in strict GitOps mode but has no repository with machinesPath, so %q is not written", clusterName, key))
		}

		return s.withBreakGlass(ctx, obj, &prPlan{repo: repo, add: true, baseBranch: orMain(repo.BaseBranch)}, bg)
	case prmode.ModeOpenPR:
		plan, err := s.planEdit(ctx, obj, repo)
		if err != nil {
			return nil, err
		}

		return s.withBreakGlass(ctx, obj, plan, bg)
	default:
		if bg != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%q is tracked by %s and no pull request can be opened for it (that needs Argo CD and a gitops.repository "+
					"on the cluster), so break-glass is refused: it would leave Git behind the cluster", key, by))
		}

		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("%q is tracked by %s, so it is read-only here: run `ichor gitops export` to put it in Git "+
				"and change it with a pull request", key, by))
	}
}

// gitOpsOf reads spec.gitops of the named cluster. A cluster that cannot be
// found is "relaxed, no repository": the write path never needed the cluster
// before, so a missing one must not newly block an untracked write.
func (s *Server) gitOpsOf(
	ctx context.Context, namespace, clusterName string,
) (repo *ichorv1alpha1.TalosClusterGitRepository, strict bool, err error) {
	var cluster ichorv1alpha1.TalosCluster

	key := types.NamespacedName{Namespace: namespace, Name: clusterName}
	if err := s.Get(ctx, key, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}

		return nil, false, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("cannot read cluster %q to tell whether GitOps is strict, so nothing is written: %w", key, err))
	}

	g := cluster.Spec.GitOps
	if g == nil {
		return nil, false, nil
	}

	return g.Repository, g.Mode == "strict", nil
}

// planEdit finds the file a tracked CR lives in: the tracking Application's
// source, which must be a plain path in the repository the cluster is
// connected to (editing another repository with this repository's token
// would be the wrong write).
func (s *Server) planEdit(
	ctx context.Context, obj client.Object, repo *ichorv1alpha1.TalosClusterGitRepository,
) (*prPlan, error) {
	key := client.ObjectKeyFromObject(obj)

	appKey, err := gitopstracking.ArgoApplicationOf(ctx, s.Client, s.argoNamespace(), obj)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("%q is tracked by Argo CD but its Application cannot be named, so it is read-only here: %w", key, err))
	}

	var app unstructured.Unstructured

	app.SetGroupVersionKind(argoApplicationGVK)

	if err := s.Client.Get(ctx, appKey, &app); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("%q is tracked by Argo CD Application %q, which cannot be read, so it is read-only here: %w", key, appKey, err))
	}

	kind, src, err := prmode.ClassifySource(&app)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if kind != prmode.SourceFile {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("%q is tracked by Argo CD Application %q, whose source is not a plain path (Helm, plugin or "+
				"multi-source), so no file can be edited: run `ichor gitops export` and apply the change by hand", key, appKey))
	}

	if !sameRepository(src.RepoURL, repo.CloneURL) {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the Argo CD Application %q syncs %q from a repository other than the one this cluster's gitops.repository "+
				"connects to, so no pull request is opened for it", appKey, src.RepoURL))
	}

	base := src.TargetRevision
	if base == "" || base == "HEAD" {
		base = orMain(repo.BaseBranch)
	}

	selfHeal, _, _ := unstructured.NestedBool(app.Object, "spec", "syncPolicy", "automated", "selfHeal")

	return &prPlan{repo: repo, sourcePath: src.Path, baseBranch: base, app: appKey.Name, selfHeal: selfHeal}, nil
}

// orMain is the CRD's own default for baseBranch, for a CR read before the
// default was applied.
func orMain(branch string) string {
	if branch == "" {
		return "main"
	}

	return branch
}

// sameRepository compares two clone URLs ignoring a trailing ".git" or "/"
// and the case of the host.
func sameRepository(a, b string) bool {
	norm := func(u string) string {
		u = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(u), "/"), ".git")

		scheme, rest, ok := strings.Cut(u, "://")
		if !ok {
			return u
		}

		host, path, _ := strings.Cut(rest, "/")

		return scheme + "://" + strings.ToLower(host) + "/" + path
	}

	return norm(a) == norm(b)
}

// openPR runs plan with change: clone, edit or add, push a branch, open the
// PR, and return its URL. Nothing is written to the cluster, and the token
// never appears in what it returns: it goes to git through an environment
// header and to the host as a request header, and is scrubbed from any error
// text anyway.
func (s *Server) openPR(ctx context.Context, plan *prPlan, change prChange) (string, error) {
	token, err := s.gitToken(ctx, plan.repo)
	if err != nil {
		return "", err
	}

	req := prmode.SubmitPRRequest{
		Host: hostOf(plan.repo.Host), APIBaseURL: strings.TrimSuffix(plan.repo.APIURL, "/"),
		Owner: plan.repo.Owner, Repo: plan.repo.Repo, CloneURL: plan.repo.CloneURL, Token: token,
		BaseBranch: plan.baseBranch,
		// Deterministic, so the same change asked twice finds the PR the first
		// one opened instead of opening another.
		HeadBranch: fmt.Sprintf("ichor/%s-%s", change.verb, change.name),
		SourcePath: plan.sourcePath, Target: change.target, Updates: change.updates, NewFile: change.newFile,
		Delete: change.remove,
		Title:  change.title, Requester: s.actorName(ctx), ImpactPreview: change.impact,
	}

	if plan.bg != nil {
		req.Title = "[break-glass] " + req.Title
		req.ImpactPreview += fmt.Sprintf("\n- Break-glass: already written to the cluster by %s, so this pull request brings Git "+
			"in line. Reason: %s\n", req.Requester, plan.bg.reason)
	}

	res, err := prmode.SubmitPR(ctx, req)
	if err != nil {
		return "", connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("opening the pull request for %s %q failed (nothing was written to the cluster): %s",
				change.verb, change.name, redact(err.Error(), token)))
	}

	return res.URL, nil
}

func hostOf(h string) prmode.Host {
	switch h {
	case "gitlab":
		return prmode.HostGitLab
	case "gitea":
		return prmode.HostGitea
	default:
		return prmode.HostGitHub
	}
}

// gitToken reads the connection's token. The Secret is looked up in the
// backend's own namespace, never in the TalosCluster's: RBAC cannot scope a
// Secret read to "wherever the cluster lives" below cluster-wide.
// An empty namespace is refused, not read as "all".
func (s *Server) gitToken(ctx context.Context, repo *ichorv1alpha1.TalosClusterGitRepository) (string, error) {
	if s.ControllerNamespace == "" {
		return "", connect.NewError(connect.CodeFailedPrecondition,
			errors.New("reading the repository token: controller namespace is empty (is POD_NAMESPACE set?)"))
	}

	ref := repo.TokenSecretRef
	key := types.NamespacedName{Namespace: s.ControllerNamespace, Name: ref.Name}

	var sec corev1.Secret

	if err := s.Client.Get(ctx, key, &sec); err != nil {
		// The error names the Secret, never its data.
		return "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("reading repository token Secret %q: %w", key, err))
	}

	token := strings.TrimSpace(string(sec.Data[ref.Key]))
	if token == "" {
		return "", connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("repository token Secret %q has no non-empty key %q", key, ref.Key))
	}

	return token, nil
}

// redact removes the token, and the base64 form git's Authorization header
// carries, from text about to be returned.
func redact(text, token string) string {
	if token == "" {
		return text
	}

	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))

	return strings.NewReplacer(token, "[redacted]", basic, "[redacted]").Replace(text)
}

// toGeneric round-trips v through JSON so the YAML editor sees the same
// field names and value shapes the API server would.
func toGeneric(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}

	return out, nil
}

func machineTarget(m *ichorv1alpha1.TalosMachine) prmode.Target {
	return prmode.Target{
		APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: kindTalosMachine, Namespace: m.Namespace, Name: m.Name,
	}
}

func patchTarget(p *ichorv1alpha1.TalosConfigPatch) prmode.Target {
	return prmode.Target{
		APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: "TalosConfigPatch", Namespace: p.Namespace, Name: p.Name,
	}
}

// machineManifest renders m as the file a strict approval adds: identity,
// endpoint and the approval, no status and no server-set metadata.
func machineManifest(m *ichorv1alpha1.TalosMachine) ([]byte, error) {
	return yaml.Marshal(map[string]any{
		"apiVersion": ichorv1alpha1.GroupVersion.String(),
		"kind":       "TalosMachine",
		"metadata":   map[string]any{"name": m.Name, "namespace": m.Namespace},
		specKey:      m.Spec,
	})
}

// machineSpecUpdates are the spec fields an approval or rejection sets, as
// edits to an existing file. Optional fields are only written when set, so a
// file that never had them is not given empty ones.
func machineSpecUpdates(m *ichorv1alpha1.TalosMachine) ([]prmode.FieldUpdate, error) {
	decision, err := toGeneric(m.Spec.ApprovalDecision)
	if err != nil {
		return nil, err
	}

	updates := []prmode.FieldUpdate{
		{Path: []string{specKey, "approvalState"}, Value: m.Spec.ApprovalState},
		{Path: []string{specKey, "approvalDecision"}, Value: decision},
	}

	if m.Spec.Role != "" {
		updates = append(updates, prmode.FieldUpdate{Path: []string{specKey, fieldRole}, Value: m.Spec.Role})
	}

	if m.Spec.Hostname != "" {
		updates = append(updates, prmode.FieldUpdate{Path: []string{specKey, "hostname"}, Value: m.Spec.Hostname})
	}

	if len(m.Spec.NodeLabels) > 0 {
		updates = append(updates, prmode.FieldUpdate{Path: []string{specKey, "nodeLabels"}, Value: m.Spec.NodeLabels})
	}

	return updates, nil
}
