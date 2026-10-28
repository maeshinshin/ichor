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
	"path"

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
// repo"). Its source is the repository root with a directory Include of
// exactly two things: every apps/<name>/application.yaml (the CNI hand-over
// Application and any later component) and the cluster's own CRs under
// clusterPath. One Application, no extra file to write, and nothing else
// (bootstrap/root-app.yaml itself, other clusters) is synced. A path of
// clusters/<name> alone, as before, never reached apps/. Ichor's CRs and
// the apps stay separate by directory and by Include.
func buildRootApplication(repoURL, clusterPath string) (*gitops.Application, error) {
	return gitops.New(gitops.Options{
		Name:    "root",
		Project: "default",
		Source: &gitops.ApplicationSource{
			RepoURL:        repoURL,
			Path:           ".",
			TargetRevision: "HEAD",
			Directory: &gitops.ApplicationSourceDir{
				Recurse: true,
				Include: "{apps/*/application.yaml," + path.Join(clusterPath, "*.yaml") + "}",
			},
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
// with --gitops-dir: the cluster's CRs under clusters/<cluster>/, the root
// Application under bootstrap/root-app.yaml when one was built, and the
// CNI's own Application under apps/cni/application.yaml when the preset
// has one (pkg/cni.Preset.ArgoCDApplication now returns pkg/gitops's own
// typed Application, so this needs no conversion).
func buildSkeleton(cluster *ichorv1alpha1.TalosCluster, rootApp, cniApp *gitops.Application, crs map[string]any) gitops.Skeleton {
	var apps map[string]*gitops.Application

	if cniApp != nil {
		apps = map[string]*gitops.Application{cniKey: cniApp}
	}

	return gitops.Skeleton{
		Cluster: cluster.Name,
		RootApp: rootApp,
		Apps:    apps,
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
// yet. What must never happen -- the credential landing in Git --
// is enforced by never writing it anywhere but this one Secret create.
func initArgoCD(
	ctx context.Context,
	opts initOptions,
	cluster *ichorv1alpha1.TalosCluster,
	machine *ichorv1alpha1.TalosMachine,
	candidates []ichorv1alpha1.TalosConfigPatch,
	cniApp *gitops.Application,
	k8sClient client.Client,
	warn io.Writer,
) error {
	if opts.RepoURL == "" {
		return fmt.Errorf("init: --repo is required with --argocd")
	}

	// Init never installs Argo CD itself -- fetching a
	// third-party install manifest during bootstrap would mean this
	// command deciding which version, signed by whom, from where, which
	// is not its call to make. Say so plainly: the root Application and
	// the repository credentials this produces do nothing on their own.
	_, _ = fmt.Fprintf(warn,
		"init: --argocd prepared the root Application and repository credentials, but did NOT install "+
			"Argo CD itself -- that is a separate, deliberate choice, not an oversight. The root "+
			"Application will do nothing until Argo CD is installed on this cluster by some other means. "+
			"This command's Application/sync-option shape was verified against the Argo CD v3.5.x docs "+
			"(argo-cd.readthedocs.io); other v3.x releases should be compatible.\n")

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
		if err := createRetrying(ctx, k8sClient, secret, warn); err != nil {
			_, _ = fmt.Fprintf(warn,
				"init: warning: could not create the Argo CD repository credentials Secret in namespace %q: %v\n"+
					"init: the credential was not written anywhere else (not into Git); create it by hand once "+
					"Argo CD is installed.\n",
				opts.ArgoCDNamespace, err)
		}
	}

	if opts.Kokumi {
		// The Secret holds no credential; the skeleton is unchanged
		// (nothing Ichor writes moves to Kokumi, and the Cilium Application stays
		// a plain Argo CD Application so it keeps ignoreDifferences).
		if err := createRetrying(ctx, k8sClient, buildKokumiRepoCredsSecret(opts.ArgoCDNamespace), warn); err != nil {
			_, _ = fmt.Fprintf(warn,
				"init: warning: could not create the Kokumi repo-creds Secret in namespace %q: %v\n", opts.ArgoCDNamespace, err)
		}

		_, _ = fmt.Fprintf(warn,
			"init: --kokumi prepared the Argo CD repo-creds Secret for Kokumi's registry, but did NOT install Kokumi "+
				"(release %s; apply its install.yaml yourself after Argo CD). Ichor's own Applications, "+
				"including Cilium, are still delivered from the repository by the root Application.\n", kokumiVersion)
	}

	if opts.GitOpsDir == "" {
		return nil
	}

	// The file names and the declared form are the ones `ichor gitops export`
	// writes (declareCRs): the live objects carry server-side metadata and,
	// from a typed client, no apiVersion/kind.
	crs := map[string]any{
		"talos-cluster.yaml":                      cluster,
		"talos-machine-" + machine.Name + ".yaml": machine,
	}

	for i := range candidates {
		crs["talos-config-patch-"+candidates[i].Name+".yaml"] = &candidates[i]
	}

	if err := declareCRs(k8sClient.Scheme(), crs); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	skeleton := buildSkeleton(cluster, rootApp, cniApp, crs)

	if err := skeleton.Write(opts.GitOpsDir); err != nil {
		return fmt.Errorf("init: writing the gitops repository skeleton: %w", err)
	}

	_, _ = fmt.Fprintf(warn, "init: gitops repository skeleton written to %s\n", opts.GitOpsDir)

	return nil
}
