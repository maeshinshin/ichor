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

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

// argoTrackingAnnotation and the rest of these are package-local aliases
// to pkg/gitopstracking's exported constants of the same meaning, kept
// purely so this package's own existing test fixtures (gitops_tracking_test.go)
// did not need editing when the rule itself moved -- the same pattern as bundleSecretKey. An alias cannot drift from
// the
// constant it points at, so this is not a second definition.
const (
	argoTrackingAnnotation      = gitopstracking.ArgoTrackingAnnotation
	argoDefaultInstanceLabelKey = gitopstracking.ArgoDefaultInstanceLabelKey
	argoConfigMapName           = gitopstracking.ArgoConfigMapName
	fluxFieldManager            = gitopstracking.FluxKustomizeControllerFieldManager
	resourceTrackingMethodKey   = gitopstracking.ResourceTrackingMethodKey
	applicationInstanceLabelKey = gitopstracking.ApplicationInstanceLabelKey
)

// isTracked reports whether obj is already tracked by Argo CD or Flux.
// The rule itself now lives in pkg/gitopstracking.IsTracked,
// shared with pkg/prmode's write-vs-PR decision; this is a one-line
// delegation so every existing call site in this package (gitops_export.go)
// and its own tests needed no changes beyond this file.
func isTracked(ctx context.Context, k8sClient client.Client, argoCDNamespace string, obj client.Object) (tracked bool, by string, err error) {
	return gitopstracking.IsTracked(ctx, k8sClient, argoCDNamespace, obj)
}
