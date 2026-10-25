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
	corev1 "k8s.io/api/core/v1"
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
// corev1, since init creates the secrets bundle Secret and, with
// --argocd, a repository credentials Secret.
func cliScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()

	if err := corev1.AddToScheme(scheme); err != nil {
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

func newKubeClientForConfig(cfg *rest.Config) (client.Client, error) {
	scheme, err := cliScheme()
	if err != nil {
		return nil, err
	}

	return client.New(cfg, client.Options{Scheme: scheme})
}
