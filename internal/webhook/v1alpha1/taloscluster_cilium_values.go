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

package v1alpha1

import (
	"encoding/json"
	"errors"
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// ciliumUserValues is spec.cni.cilium.values as a map; nil when unset.
func ciliumUserValues(c *ichorv1alpha1.TalosCluster) (map[string]any, error) {
	cc := c.Spec.CNI.Cilium
	if cc == nil || cc.Values == nil || len(cc.Values.Raw) == 0 {
		return nil, nil
	}

	var user map[string]any
	if err := json.Unmarshal(cc.Values.Raw, &user); err != nil {
		return nil, errors.New("must be a JSON object (the Helm values of the Cilium chart)")
	}

	return user, nil
}

// validateCiliumValues refuses the Cilium values the UI and the CLI would refuse (ADR 0129): the chart
// schema, unknown keys, keys Ichor owns and the size cap, with spec.cni.kubeProxy and spec.cni.tunnel
// selecting Ichor's own layer. The guards that need the machine configuration or the live cluster
// (hostDNS, KubePrism, Gateway API CRDs) are left unknown here and stay with the UI and the CLI.
//
// On update it runs only when the values, the kube-proxy mode or the tunnel changes, so a stored
// cluster that predates a stricter check can still be edited for something else. An adopted cluster
// whose CNI Ichor leaves alone has no Cilium values of Ichor's.
func validateCiliumValues(oldObj, newObj *ichorv1alpha1.TalosCluster) error {
	if newObj.Spec.CNI.Preset == presetUnmanaged || removedPreset(newObj) {
		return nil
	}

	user, err := ciliumUserValues(newObj)
	if err != nil {
		return fmt.Errorf("spec.cni.cilium.values: %w", err)
	}

	if oldObj != nil && !ciliumInputsChanged(oldObj, newObj, user) {
		return nil
	}

	if oldObj == nil && user == nil {
		return nil
	}

	kubeProxy := cni.KubeProxyMode(newObj.Spec.CNI.KubeProxy)
	if kubeProxy == "" {
		kubeProxy = cni.KubeProxyEnabled
	}

	if err := cilium.ValidateValues(user, cilium.ValidationContext{KubeProxy: kubeProxy, Tunnel: newObj.Spec.CNI.Tunnel}); err != nil {
		return fmt.Errorf("spec.cni.cilium.values is not valid (the same check as the UI and `ichor cni values set`):\n%w", err)
	}

	return nil
}

// ciliumInputsChanged reports whether an update changes what ValidateValues reads.
func ciliumInputsChanged(oldObj, newObj *ichorv1alpha1.TalosCluster, user map[string]any) bool {
	if oldObj.Spec.CNI.KubeProxy != newObj.Spec.CNI.KubeProxy || oldObj.Spec.CNI.Tunnel != newObj.Spec.CNI.Tunnel {
		return true
	}

	// A stored object that does not decode counts as changed: the new one is checked.
	stored, err := ciliumUserValues(oldObj)

	return err != nil || !apiequality.Semantic.DeepEqual(stored, user)
}
