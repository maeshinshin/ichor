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

// newKubeClient builds a controller-runtime client scoped to Ichor's own
// CRDs -- adopt reads and writes TalosCluster, TalosMachine and
// TalosConfigPatch only, never a core resource, so that is all the scheme
// needs to know about.
func newKubeClient(kubeconfigPath string) (client.Client, error) {
	cfg, err := loadKubeconfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return client.New(cfg, client.Options{Scheme: scheme})
}
