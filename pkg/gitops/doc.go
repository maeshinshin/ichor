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

// Package gitops builds Argo CD Application manifests and the GitOps
// repository skeleton described in docs/design/architecture.md ("GitOps and
// Argo CD"), as plain files on disk. It makes no Argo CD, Git or Kubernetes
// API calls: everything here is pure construction and YAML encoding, so that
// both `ichor init --gitops-dir` and `ichor gitops export` (neither built in
// this package) can drive it the same way.
//
// Argo CD's own Application type (argoproj/argo-cd/pkg/apis/application/v1alpha1)
// is deliberately not imported. Trying to add it as a dependency (checked
// against v3.5.6, the latest tag as of this package's writing) fails outright:
// that package imports argo-cd's vendored fork of gitops-engine through a
// module path (github.com/argoproj/argo-cd/gitops-engine) that has no go.mod
// at the revision argo-cd's own go.mod pins, so `go get` cannot resolve it
// from outside the argo-cd repository. Even if that were fixed, the same
// package pulls in util/git and util/exec, dragging in go-git, an SSH agent
// client, cloud provider SDKs and a Redis cache client -- none of which this
// package, which only ever marshals YAML to a local file, has any use for.
// The subset of the Application schema actually needed (apiVersion/kind,
// project, source(s), destination, syncPolicy, ignoreDifferences) is small
// and stable enough to hand-maintain against the public docs instead.
package gitops
