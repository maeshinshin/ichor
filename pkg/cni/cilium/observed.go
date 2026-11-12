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
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/maeshinshin/ichor/pkg/values"
)

const (
	configMapName        = "cilium-config"
	mountCgroupContainer = "mount-cgroup"
	cleanStateContainer  = "clean-cilium-state"
	envKubernetesHost    = "KUBERNETES_SERVICE_HOST"
	envKubernetesPort    = "KUBERNETES_SERVICE_PORT"

	keyIPAMMode       = "ipam.mode"
	keyHostRoot       = "cgroup.hostRoot"
	keyAutoMount      = "cgroup.autoMount.enabled"
	keyServiceHost    = "k8sServiceHost"
	keyServicePort    = "k8sServicePort"
	keyCapsAgent      = "securityContext.capabilities.ciliumAgent"
	keyCapsCleanState = "securityContext.capabilities.cleanCiliumState"

	// The flag that keeps the agent's Secret read to cilium-secrets and its deprecated alias.
	keyReadSecretsOnly = "tls.readSecretsOnlyFromSecretsNamespace"
	keySecretsBackend  = "tls.secretsBackend"
	// cmReadSecretsOnly is the cilium-config key the chart writes, as "true", only while the flag is true.
	cmReadSecretsOnly = "policy-secrets-only-from-secrets-namespace"

	valueTrue = "true"
)

// ConfigMapValues maps the keys of the cilium-config ConfigMap to the Helm value they render
// from (chart 1.18, templates/cilium-configmap.yaml). It is the one table for case C: a Cilium
// that came from a Talos inline manifest has no release record, so the live values of the
// Ichor-owned keys are read back from what runs instead of assumed from the chart defaults.
// routing-mode is not owned but is read so a native-routing cluster is refused.
var ConfigMapValues = map[string]string{
	"ipam":                   keyIPAMMode,
	"tunnel-protocol":        keyTunnelProtocol,
	"routing-mode":           "routingMode",
	"kube-proxy-replacement": keyKubeProxyReplacement,
	"cgroup-root":            keyHostRoot,

	// The pinned secretsNamespace names. Each is in the ConfigMap only while its feature is on,
	// and the feature switch is read with it so that a feature that is off is not reported as a conflict.
	"ingress-secrets-namespace":     "ingressController.secretsNamespace.name",
	"enable-ingress-controller":     "ingressController.enabled",
	"gateway-api-secrets-namespace": "gatewayAPI.secretsNamespace.name",
	"enable-gateway-api":            "gatewayAPI.enabled",
	"envoy-secrets-namespace":       "envoyConfig.secretsNamespace.name",
	"bgp-secrets-namespace":         "bgpControlPlane.secretsNamespace.name",
	"enable-bgp-control-plane":      "bgpControlPlane.enabled",
	"policy-secrets-namespace":      "tls.secretsNamespace.name",

	// Written only while the flag is true, so overlayObserved reads its absence as false.
	cmReadSecretsOnly: keyReadSecretsOnly,
}

// LiveValuesFromCluster is LiveValuesFromUser with the Ichor-owned keys replaced by what the
// cluster runs: the cilium-config ConfigMap (ConfigMapValues) and the cilium DaemonSet (service
// host and port from the agent's environment, the capability lists, and whether the
// mount-cgroup init container exists, which is cgroup.autoMount.enabled). A missing ConfigMap or
// DaemonSet leaves the chart defaults for its keys; the diff gate then shows the object missing.
func LiveValuesFromCluster(
	ctx context.Context, kube kubernetes.Interface, user map[string]any,
) (map[string]any, error) {
	vals, err := LiveValuesFromUser(user)
	if err != nil {
		return nil, err
	}

	cm, err := kube.CoreV1().ConfigMaps(ReleaseNamespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("cilium: reading the %s ConfigMap: %w", configMapName, err)
	}

	ds, err := kube.AppsV1().DaemonSets(ReleaseNamespace).Get(ctx, ReleaseName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("cilium: reading the cilium DaemonSet: %w", err)
	}

	overlayObserved(vals, cm, ds)

	return vals, nil
}

// overlayObserved writes the observed live values into vals. cm and ds may be nil or empty.
func overlayObserved(vals map[string]any, cm *corev1.ConfigMap, ds *appsv1.DaemonSet) {
	if cm != nil {
		for key, path := range ConfigMapValues {
			if v, ok := cm.Data[key]; ok {
				values.Set(vals, path, v)
			}
		}

		// shortcut: assumes a chart that writes the key while the flag is true (1.19 and 1.20 do).
		if cm.Name != "" {
			if _, ok := cm.Data[cmReadSecretsOnly]; !ok {
				values.Set(vals, keyReadSecretsOnly, false)
			}
		}
	}

	if ds == nil || ds.Name == "" {
		return
	}

	spec := ds.Spec.Template.Spec
	host, port := "", ""
	autoMount := false

	for _, c := range spec.Containers {
		if c.Name != agentContainerName {
			continue
		}

		for _, e := range c.Env {
			switch e.Name {
			case envKubernetesHost:
				host = e.Value
			case envKubernetesPort:
				port = e.Value
			}
		}

		values.Set(vals, keyCapsAgent, capabilityList(c))
	}

	for _, c := range spec.InitContainers {
		switch c.Name {
		case mountCgroupContainer:
			autoMount = true
		case cleanStateContainer:
			values.Set(vals, keyCapsCleanState, capabilityList(c))
		}
	}

	values.Set(vals, keyServiceHost, host)
	values.Set(vals, keyServicePort, port)
	values.Set(vals, keyAutoMount, autoMount)
}

func capabilityList(c corev1.Container) []any {
	out := []any{}

	if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil {
		return out
	}

	for _, name := range slices.Sorted(slices.Values(c.SecurityContext.Capabilities.Add)) {
		out = append(out, string(name))
	}

	return out
}
