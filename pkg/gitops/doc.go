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

// Package gitops holds the one piece of Argo CD's Application schema Ichor
// reads: ApplicationSource, which pkg/prmode classifies to decide how a
// tracked CR's source is edited. It builds nothing and makes no Argo CD, Git
// or Kubernetes API calls.
//
// Argo CD's own Application type (argoproj/argo-cd/pkg/apis/application/v1alpha1)
// is deliberately not imported: it drags in go-git, an SSH agent client, cloud
// provider SDKs and a Redis cache client, and its gitops-engine fork cannot be
// resolved from outside the argo-cd repository. The few fields needed are
// hand-maintained against the public docs instead.
package gitops
