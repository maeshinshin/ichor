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

package chart

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

// firstContainer is the first container of a Deployment document.
func firstContainer(d map[string]any) map[string]any {
	return podSpecOf(d)["containers"].([]any)[0].(map[string]any)
}

// deploymentsByInstall returns the Deployment named by chart suffix from the chart and from the kustomize base.
func deploymentsByInstall(t *testing.T, suffix, basePath string) map[string]map[string]any {
	t.Helper()

	out := map[string]map[string]any{labelChart: findNameSuffix(renderChart(t, "ichor"), kindDeployment, suffix)}

	for _, d := range loadYAMLDocs(t, basePath) {
		if d["kind"] == kindDeployment {
			out[labelConfig] = d
		}
	}

	return out
}

func probeField(c map[string]any, probe, field string) float64 {
	v, _ := c[probe].(map[string]any)[field].(float64)

	return v
}

// TestControllerSurvivesALoadedCluster: hardware run I saw the controller OOM-killed at 256Mi and restarted
// by a 1 s liveness timeout, which takes the fail-closed admission webhook down with it. Both install paths
// carry the larger limit, a soft Go memory limit below it, and probe timeouts that ride out a CPU-starved pod.
func TestControllerSurvivesALoadedCluster(t *testing.T) {
	for label, d := range deploymentsByInstall(t, "controller-manager", "../../config/manager/manager.yaml") {
		c := firstContainer(d)

		limits := c["resources"].(map[string]any)["limits"].(map[string]any)
		mem := resource.MustParse(limits["memory"].(string))

		if min := resource.MustParse("512Mi"); mem.Cmp(min) < 0 {
			t.Errorf("%s: memory limit = %s, want at least 512Mi", label, mem.String())
		}

		var goLimit string

		for _, e := range c["env"].([]any) {
			if m := e.(map[string]any); m["name"] == "GOMEMLIMIT" {
				goLimit, _ = m["value"].(string)
			}
		}

		if goLimit == "" {
			t.Errorf("%s: no GOMEMLIMIT: the Go heap must stay below the container limit instead of growing into it", label)
		} else if q := resource.MustParse(strings.TrimSuffix(goLimit, "B")); q.Cmp(mem) >= 0 { // Go's "400MiB"
			t.Errorf("%s: GOMEMLIMIT %s is not below the memory limit %s", label, goLimit, mem.String())
		}

		for _, probe := range []string{"livenessProbe", "readinessProbe"} {
			if got := probeField(c, probe, "timeoutSeconds"); got < 5 {
				t.Errorf("%s: %s timeoutSeconds = %v, want at least 5", label, probe, got)
			}
		}

		if got := probeField(c, "livenessProbe", "failureThreshold"); got < 3 {
			t.Errorf("%s: livenessProbe failureThreshold = %v, want at least 3", label, got)
		}
	}
}

// TestUIProbesRideOutCPUStarvation: the one-second default timeout of the ichor-ui probes is as tight as the
// controller's was, and the UI hashes passwords with argon2id on the same 500m CPU.
func TestUIProbesRideOutCPUStarvation(t *testing.T) {
	for label, d := range deploymentsByInstall(t, "ichor-ui", "../../config/manager-ui/manager.yaml") {
		c := firstContainer(d)

		for _, probe := range []string{"livenessProbe", "readinessProbe"} {
			if got := probeField(c, probe, "timeoutSeconds"); got < 5 {
				t.Errorf("%s: ichor-ui %s timeoutSeconds = %v, want at least 5", label, probe, got)
			}
		}
	}
}
