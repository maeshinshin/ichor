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

package cilium

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// RecordNamespace is where Ichor keeps the Helm release record of Cilium (ichor-system).
const RecordNamespace = "ichor-system"

// nodePollInterval is how often Bootstrap looks at the nodes while it waits for them to be Ready.
const nodePollInterval = 5 * time.Second

// ApplyForCluster runs Apply with the effective values of cluster in kube-proxy mode mode: the
// one place that turns a TalosCluster into the release, for the ichor-cni Job and for Bootstrap
// alike, so what the CLI installs is what the controller expects to find.
func ApplyForCluster(
	ctx context.Context, cfg *action.Configuration, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, timeout time.Duration,
) (ApplyResult, error) {
	vals, err := EffectiveValuesForMode(cluster, mode)
	if err != nil {
		return ApplyResult{}, err
	}

	return Apply(ctx, cfg, vals, ApplyOptions{Timeout: timeout})
}

// Bootstrap installs Ichor's Cilium on a cluster that has no CNI, from the operator's machine: it creates
// ichor-system for the release record, applies the release for
// cluster, and waits, bounded by timeout, for the DaemonSet (Helm's own wait) and then every node to
// be Ready. It is safe to run again: Apply upgrades an existing release.
func Bootstrap(
	ctx context.Context, rc *rest.Config, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, timeout time.Duration,
) (ApplyResult, error) {
	kube, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("cilium: building a Kubernetes client: %w", err)
	}

	cfg := new(action.Configuration)

	if err := cfg.Init(restGetter{rc: rc}, RecordNamespace, "secret", func(string, ...any) {}); err != nil {
		return ApplyResult{}, fmt.Errorf("cilium: configuring Helm: %w", err)
	}

	return bootstrap(ctx, kube, cfg, cluster, mode, timeout, nodePollInterval)
}

func bootstrap(
	ctx context.Context, kube kubernetes.Interface, cfg *action.Configuration, cluster *ichorv1alpha1.TalosCluster,
	mode cni.KubeProxyMode, timeout, interval time.Duration,
) (ApplyResult, error) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: RecordNamespace}}
	if _, err := kube.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return ApplyResult{}, fmt.Errorf("cilium: creating namespace %s: %w", RecordNamespace, err)
	}

	res, err := ApplyForCluster(ctx, cfg, cluster, mode, timeout)
	if err != nil {
		return res, err
	}

	var notReady []string

	err = wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		nodes, err := kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil //nolint:nilerr // a flaky API server is retried until the timeout
		}

		notReady = notReady[:0]

		for i := range nodes.Items {
			if !nodeReady(&nodes.Items[i]) {
				notReady = append(notReady, nodes.Items[i].Name)
			}
		}

		return len(nodes.Items) > 0 && len(notReady) == 0, nil
	})
	if err != nil {
		return res, fmt.Errorf("cilium: nodes not Ready within %s after installing Cilium (still NotReady: %s): %w",
			timeout, strings.Join(notReady, ", "), err)
	}

	return res, nil
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}

// restGetter lets Helm talk to the cluster a rest.Config reaches (the CLI's kubeconfig, exec
// plugins included), without going through a kubeconfig file again.
type restGetter struct{ rc *rest.Config }

func (g restGetter) ToRESTConfig() (*rest.Config, error) { return rest.CopyConfig(g.rc), nil }

func (g restGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(g.rc)
	if err != nil {
		return nil, err
	}

	return memory.NewMemCacheClient(dc), nil
}

func (g restGetter) ToRESTMapper() (meta.RESTMapper, error) {
	dc, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	return restmapper.NewDeferredDiscoveryRESTMapper(dc), nil
}

func (g restGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.NewDefaultClientConfig(*api.NewConfig(), &clientcmd.ConfigOverrides{Context: api.Context{Namespace: ReleaseNamespace}})
}
