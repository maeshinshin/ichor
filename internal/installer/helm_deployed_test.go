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

package installer

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// The installer Job refuses a Helm-owned controller: by the Helm label or by the
// --deployed-by=helm flag the chart passes.
func TestHelmDeployed(t *testing.T) {
	dep := func(labels map[string]string, args ...string) *appsv1.Deployment {
		d := &appsv1.Deployment{}
		d.Labels = labels
		d.Spec.Template.Spec.Containers = []corev1.Container{{Name: ControllerContainer, Args: args}}

		return d
	}

	for name, tc := range map[string]struct {
		d    *appsv1.Deployment
		want bool
	}{
		"absent":           {nil, false},
		"plain":            {dep(nil, "--leader-elect"), false},
		"installer flag":   {dep(nil, "--deployed-by=installer"), false},
		"helm label":       {dep(map[string]string{"app.kubernetes.io/managed-by": "Helm"}), true},
		"helm flag":        {dep(nil, "--leader-elect", "--deployed-by=helm"), true},
		"other managed-by": {dep(map[string]string{"app.kubernetes.io/managed-by": "kustomize"}), false},
	} {
		if got := helmDeployed(tc.d); got != tc.want {
			t.Errorf("%s: helmDeployed = %v, want %v", name, got, tc.want)
		}
	}
}
