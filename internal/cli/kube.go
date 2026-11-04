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
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// loadKubeconfig resolves a kubeconfig the same way kubectl does: an
// explicit path wins, otherwise client-go's own loading rules fall back to
// $KUBECONFIG and then ~/.kube/config.
func loadKubeconfig(path string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path != "" {
		rules.ExplicitPath = path
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
}

// cliScheme is every type a CLI command needs a typed client for: Ichor's
// own CRDs (TalosCluster, TalosMachine, TalosConfigPatch, ...) plus
// corev1, since init creates the secrets bundle Secret.
func cliScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	// apps/v1: init turns on users by editing the ichor-ui Deployment (ADR 0094).
	if err := appsv1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	// discovery/v1: init and adopt wait for the webhook Service's ready endpoint.
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	// authorization/v1: local mode runs the uiapi server in-process, and its
	// override and diagnostics checks create SelfSubjectAccessReviews.
	if err := authv1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	// rbac/v1: `ichor upgrade ichor` lists roles that still grant the earlier override verb.
	if err := rbacv1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return scheme, nil
}

// newKubeClient builds a controller-runtime client from the kubeconfig at
// kubeconfigPath (adopt's path: the cluster it is adopting already has
// one).
func newKubeClient(kubeconfigPath string) (client.Client, error) {
	cfg, err := loadKubeconfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	return newKubeClientForConfig(cfg)
}

// newKubeClientFromBytes builds a controller-runtime client from raw
// kubeconfig bytes (init's path: the kubeconfig came back from
// Kubeconfig(), the node Talos just bootstrapped, not a file on disk).
func newKubeClientFromBytes(data []byte) (client.Client, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, err
	}

	return newKubeClientForConfig(cfg)
}

// newKubeClientForConfig is the one place every CLI command's
// Kubernetes client comes from. It requests strict field validation
// (architecture.md's "Typed configuration" section: no admission webhook
// for this, "Ichor's own client" is strict instead) -- the API server's
// plain behavior for an unknown field is to prune it silently (an "x-kubernetes-
// pruning"-generated warning header, not an error; confirmed by trying
// it, not assumed), which would let a typo or a since-dropped field name
// (e.g. one of the nine secret fields removed from the Config mirror
// type) disappear into a CR with no sign anything was wrong. kubectl
// already defaults to strict, so a human applying the same YAML by hand
// is already covered; this is the one gap that was `ichor`'s own clients
// specifically.
func newKubeClientForConfig(cfg *rest.Config) (client.Client, error) {
	scheme, err := cliScheme()
	if err != nil {
		return nil, err
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme, FieldValidation: metav1.FieldValidationStrict})
	if err != nil {
		return nil, err
	}

	// The update notice (update_notice.go) reads the installation through the last cluster a command used.
	lastCluster = noticeTarget{Host: cfg.Host, Client: c}

	return c, nil
}
