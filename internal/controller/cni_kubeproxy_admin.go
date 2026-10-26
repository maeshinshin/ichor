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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// kubeconfigKubeProxyAdmin is the real KubeProxyAdmin. Like kubeconfigAdmin it goes through an
// admin kubeconfig the Talos API issues, so the controller's own ServiceAccount needs no right
// to delete DaemonSets or ClusterRoleBindings.
type kubeconfigKubeProxyAdmin struct{}

// NewKubeProxyAdmin returns the KubeProxyAdmin cmd/main.go wires in.
func NewKubeProxyAdmin() KubeProxyAdmin { return kubeconfigKubeProxyAdmin{} }

// RemoveKubeProxy deletes the four kinds of object Talos v1.14.3's ManifestController renders
// for kube-proxy (the "10-kube-proxy" manifest): the DaemonSet, ServiceAccount and
// ClusterRoleBinding by their fixed name, and the checksum-named ConfigMaps by their labels.
// ManifestApplyController has no delete path, so nothing else will.
//
// ponytail: the nftables/iptables rules kube-proxy already programmed stay on each node until the
// node reboots; there is no per-node cleanup.
func (kubeconfigKubeProxyAdmin) RemoveKubeProxy(ctx context.Context, talos TalosClient) error {
	cfg, err := restConfig(ctx, talos)
	if err != nil {
		return err
	}

	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("building a client from the admin kubeconfig: %w", err)
	}

	meta := metav1.ObjectMeta{Namespace: kubeSystemNamespace, Name: kubeProxyName}
	background := client.PropagationPolicy(metav1.DeletePropagationBackground)

	for _, o := range []client.Object{
		&appsv1.DaemonSet{ObjectMeta: meta},
		&corev1.ServiceAccount{ObjectMeta: meta},
		&rbacv1.ClusterRoleBinding{Name: kubeProxyName},
	} {
		if err := c.Delete(ctx, o, background); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting %T %q: %w", o, kubeProxyName, err)
		}
	}

	if err := c.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace(kubeSystemNamespace),
		client.MatchingLabels{labelK8sApp: kubeProxyName, "tier": "node"}); err != nil {
		return fmt.Errorf("deleting the kube-proxy ConfigMaps: %w", err)
	}

	return nil
}
