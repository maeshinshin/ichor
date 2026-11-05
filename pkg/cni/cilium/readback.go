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
	"errors"
	"fmt"
	"reflect"

	"helm.sh/helm/v3/pkg/release"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// ReadBack is what an Ichor-owned Cilium release was installed with, in the terms of the
// TalosCluster that would reproduce it.
type ReadBack struct {
	KubeProxy cni.KubeProxyMode
	// Tunnel is "vxlan", "geneve" or "" (the release sets none).
	Tunnel string
	// ControlPlanes is 1 when the record pins the operator to one replica (one control plane), else 0.
	ControlPlanes int
	// User are the values beyond Ichor's own layers (spec.cni.cilium.values); empty when none.
	User map[string]any
	// Pending is true when the record is pending-install/upgrade/rollback: an operation was killed.
	Pending bool
}

// ReadBackRelease reads the tunnel, the kube-proxy mode and the user values out of Ichor's own
// Cilium release record, so that EffectiveValues(User, KubeProxy, Tunnel) equals the values the
// release was applied with. The record's Config holds Ichor's complete effective values
// (ApplyForCluster), so Ichor's defaults and owned keys are subtracted to leave the user's.
func ReadBackRelease(rel *release.Release) (ReadBack, error) {
	if rel == nil {
		return ReadBack{}, errors.New("cilium: no release record to read")
	}

	cfg := values.Clone(rel.Config)

	rb := ReadBack{KubeProxy: cni.KubeProxyEnabled, Pending: rel.Info != nil && rel.Info.Status.IsPending()}
	if v, _ := cfg["kubeProxyReplacement"].(bool); v {
		rb.KubeProxy = cni.KubeProxyReplaced
	}

	rb.Tunnel, _ = cfg["tunnelProtocol"].(string)

	// The record's operator replica count says whether it was made for one control plane (values.go).
	if op, ok := cfg["operator"].(map[string]any); ok && fmt.Sprint(op["replicas"]) == "1" {
		rb.ControlPlanes = 1
	}

	p, err := newPreset(rb.KubeProxy, rb.Tunnel, rb.ControlPlanes)
	if err != nil {
		return ReadBack{}, err
	}

	defaults, _, err := p.layers(rb.KubeProxy)
	if err != nil {
		return ReadBack{}, err
	}

	rb.User = subtract(UserValues(cfg), defaults)

	return rb, nil
}

// subtract removes from m every leaf that equals the one in base, and the maps it empties.
func subtract(m, base map[string]any) map[string]any {
	for k, v := range m {
		b, ok := base[k]
		if !ok {
			continue
		}

		if vm, isMap := v.(map[string]any); isMap {
			if bm, bIsMap := b.(map[string]any); bIsMap && len(subtract(vm, bm)) == 0 {
				delete(m, k)
			}

			continue
		}

		if reflect.DeepEqual(v, b) {
			delete(m, k)
		}
	}

	return m
}
