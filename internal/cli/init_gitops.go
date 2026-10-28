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

// ponytail: ichor init no longer uses anything here (ADR 0099 unit 10); these two builders
// are left only for `ichor gitops export`, and go with it in unit 16.

import (
	"path"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitops"
)

// argoCDDestinationServer is the in-cluster API server address Argo CD
// Applications target when Ichor is self-managed (the default deployment
// model, architecture.md's "Deployment"): the same literal
// pkg/cni/cilium's own ArgoCDApplication uses.
const argoCDDestinationServer = "https://kubernetes.default.svc"

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

// with --gitops-dir: the cluster's CRs under clusters/<cluster>/, the root
// Application under bootstrap/root-app.yaml when one was built, and the
// CNI's own Application under apps/cni/application.yaml when the preset
// has one (pkg/cni.Preset.ArgoCDApplication now returns pkg/gitops's own
// typed Application, ADR 0013's update, so this needs no conversion).
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
