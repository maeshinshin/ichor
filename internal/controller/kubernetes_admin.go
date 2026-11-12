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

package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// kubeconfigAdmin is the real KubernetesAdmin: both actions go through an admin kubeconfig the
// Talos API issues (TalosClient.Kubeconfig), the way talosctl upgrade-k8s reaches the cluster, so
// the controller's own ServiceAccount does not need to be allowed to write ClusterRoles,
// DaemonSets or CRDs.
type kubeconfigAdmin struct{}

// NewKubernetesAdmin returns the KubernetesAdmin cmd/main.go wires in.
func NewKubernetesAdmin() KubernetesAdmin { return kubeconfigAdmin{} }

func restConfig(ctx context.Context, talos TalosClient) (*rest.Config, error) {
	kubeconfig, err := talos.Kubeconfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching an admin kubeconfig from the Talos API: %w", err)
	}

	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parsing the admin kubeconfig: %w", err)
	}

	return cfg, nil
}

// SyncBootstrapManifests server-side-applies the node's bootstrap manifest set under Talos's own
// field manager, so ownership carries over from the apply Talos did at bootstrap.
//
// shortcut: no pruning and no inventory. talosctl upgrade-k8s (go-kubernetes' ssa.Manager) also
// deletes objects that dropped out of the set and waits for each to reconcile; here an object a
// later Talos version stops shipping stays until removed by hand, and the caller checks only
// kube-proxy's rollout. Depending on go-kubernetes for that bumps the k8s.io/* modules the whole
// controller builds against.
func (kubeconfigAdmin) SyncBootstrapManifests(ctx context.Context, talos TalosClient) error {
	cfg, err := restConfig(ctx, talos)
	if err != nil {
		return err
	}

	objects, err := talos.BootstrapManifests(ctx)
	if err != nil {
		return err
	}

	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("building a client from the admin kubeconfig: %w", err)
	}

	for _, o := range objects {
		u := &unstructured.Unstructured{Object: o}
		u.SetManagedFields(nil)

		if err := c.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), client.ForceOwnership, client.FieldOwner(constants.KubernetesFieldManagerName)); err != nil {
			return fmt.Errorf("applying %s %q: %w", u.GetKind(), u.GetName(), err)
		}
	}

	return nil
}

// RemovedAPIsInUse reads the API server's own deprecated-API request metric.
func (kubeconfigAdmin) RemovedAPIsInUse(ctx context.Context, talos TalosClient, targetMinor string) ([]string, error) {
	cfg, err := restConfig(ctx, talos)
	if err != nil {
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building a clientset from the admin kubeconfig: %w", err)
	}

	body, err := cs.CoreV1().RESTClient().Get().AbsPath("/metrics").DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the API server's metrics: %w", err)
	}

	return upgrade.RemovedAPIsInUse(string(body), targetMinor)
}
