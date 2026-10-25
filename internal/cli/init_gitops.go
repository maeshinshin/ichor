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

package cli

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitops"
)

// argoCDDestinationServer is the in-cluster API server address Argo CD
// Applications target when Ichor is self-managed (the default deployment
// model, architecture.md's "Deployment"): the same literal
// pkg/cni/cilium's own ArgoCDApplication uses.
const argoCDDestinationServer = "https://kubernetes.default.svc"

// repositorySecretTypeLabel and repositorySecretType mark a Secret as an
// Argo CD repository credential (argo-cd.readthedocs.io,
// "Private Repositories", retrieved 2026-10-25): the label is what Argo CD's
// repo-server scans for, "git" is the only source kind init ever registers
// a credential for.
const (
	repositorySecretTypeLabel = "argocd.argoproj.io/secret-type"
	repositorySecretTypeValue = "repository"
	repositorySourceType      = "git"
)

// buildRootApplication builds the app-of-apps root Application design
// step 3 describes ("Argo CD plus a root Application pointing at the
// repo"): a Directory source over repoPath, recursing so every
// apps/<name>/application.yaml the skeleton writes is picked up
// automatically.
func buildRootApplication(repoURL, repoPath string) (*gitops.Application, error) {
	return gitops.New(gitops.Options{
		Name:    "root",
		Project: "default",
		Source: &gitops.ApplicationSource{
			RepoURL:        repoURL,
			Path:           repoPath,
			TargetRevision: "HEAD",
			Directory:      &gitops.ApplicationSourceDir{Recurse: true},
		},
		Destination: gitops.ApplicationDestination{Server: argoCDDestinationServer},
		Automated:   true,
		Prune:       true,
		SelfHeal:    true,
		SyncWave:    gitops.SyncWaveArgoCD,
	})
}

// buildRepoSecret builds the Argo CD repository-credentials Secret design
// step 3 requires ("The repo credentials go in as a Secret from the CLI,
// never into Git"). It returns nil, nil when neither username nor
// password was given: a public repository needs no credential, and this
// command does not create an empty, useless Secret just to have one.
func buildRepoSecret(namespace, repoURL, username, password string) (*corev1.Secret, error) {
	if username == "" && password == "" {
		return nil, nil //nolint:nilnil // "no credential given" is a legitimate non-error outcome, not nil,nil-as-placeholder
	}

	if repoURL == "" {
		return nil, fmt.Errorf("init: --repo is required to create a repository credentials Secret")
	}

	return &corev1.Secret{
		GenerateName: "repo-",
		Namespace:    namespace,
		Labels:       map[string]string{repositorySecretTypeLabel: repositorySecretTypeValue},
		StringData: map[string]string{
			"type":     repositorySourceType,
			"url":      repoURL,
			"username": username,
			"password": password,
		},
	}, nil
}

// buildSkeleton assembles the pkg/gitops.Skeleton design step 5 writes
// with --gitops-dir: the cluster's CRs under clusters/<cluster>/, and the
// root Application under bootstrap/root-app.yaml when one was built.
//
// It does not yet write apps/cni/application.yaml: pkg/cni.Preset's
// ArgoCDApplication returns *unstructured.Unstructured, and
// pkg/gitops.Skeleton.Apps takes gitops.Application -- two different,
// independently-owned representations of an Argo CD Application with no
// conversion between them today. Writing the CNI's Application into the
// GitOps skeleton needs that conversion to exist first; see ADR 0019.
func buildSkeleton(cluster *ichorv1alpha1.TalosCluster, rootApp *gitops.Application, crs map[string]any) gitops.Skeleton {
	return gitops.Skeleton{
		Cluster: cluster.Name,
		RootApp: rootApp,
		CRs:     crs,
	}
}

// initArgoCD is runInit's --argocd handling: a best-effort repository
// credentials Secret (design step 3: "The repo credentials go in as a
// Secret from the CLI, never into Git"), and, with --gitops-dir, the
// repository skeleton (design step 5).
//
// The Secret create is soft-fail, logged to warn rather than returned:
// by the time this runs, the node has already been configured and
// bootstrapped and the CRs already created, and none of that should be
// treated as failed just because Argo CD's own namespace does not exist
// yet (ADR 0019 -- bootstrapping Argo CD itself is not this command's
// job today). What must never happen -- the credential landing in Git --
// is enforced by never writing it anywhere but this one Secret create.
func initArgoCD(
	ctx context.Context,
	opts initOptions,
	cluster *ichorv1alpha1.TalosCluster,
	machine *ichorv1alpha1.TalosMachine,
	candidates []ichorv1alpha1.TalosConfigPatch,
	k8sClient client.Client,
	warn io.Writer,
) error {
	if opts.RepoURL == "" {
		return fmt.Errorf("init: --repo is required with --argocd")
	}

	// Build (and so validate) the root Application before creating
	// anything: gitops.New rejects a RepoURL carrying embedded userinfo
	// (pkg/gitops/application.go's checkRepoURL), which is the second of
	// the two places that guarantee a repository credential never reaches
	// Git. Checking it before the repo Secret create below means a bad
	// --repo value is caught before any side effect, not after.
	rootApp, err := buildRootApplication(opts.RepoURL, opts.RepoPath)
	if err != nil {
		return fmt.Errorf("init: building the root Application: %w", err)
	}

	secret, err := buildRepoSecret(opts.ArgoCDNamespace, opts.RepoURL, opts.RepoUsername, opts.RepoPassword)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if secret != nil {
		if err := k8sClient.Create(ctx, secret); err != nil {
			_, _ = fmt.Fprintf(warn,
				"init: warning: could not create the Argo CD repository credentials Secret in namespace %q: %v\n"+
					"init: the credential was not written anywhere else (not into Git); create it by hand once "+
					"Argo CD is installed.\n",
				opts.ArgoCDNamespace, err)
		}
	}

	if opts.GitOpsDir == "" {
		return nil
	}

	crs := map[string]any{
		"talos-cluster.yaml": cluster,
		"talos-machine.yaml": machine,
	}

	for i, p := range candidates {
		crs[fmt.Sprintf("talos-config-patch-%d.yaml", i)] = &p
	}

	skeleton := buildSkeleton(cluster, rootApp, crs)

	if err := skeleton.Write(opts.GitOpsDir); err != nil {
		return fmt.Errorf("init: writing the gitops repository skeleton: %w", err)
	}

	_, _ = fmt.Fprintf(warn, "init: gitops repository skeleton written to %s\n", opts.GitOpsDir)

	return nil
}
