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
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

func TestOnDeleteRemedyDeletesPodsInsteadOfRolloutRestart(t *testing.T) {
	ds := &appsv1.DaemonSet{
		Spec: appsv1.DaemonSetSpec{
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{Type: appsv1.OnDeleteDaemonSetStrategyType},
		},
		Status: appsv1.DaemonSetStatus{
			ObservedGeneration: 1, DesiredNumberScheduled: 2, NumberReady: 2, UpdatedNumberScheduled: 0,
		},
	}

	ds.Name, ds.Namespace, ds.Generation = ReleaseName, ReleaseNamespace, 1

	ok, why := DaemonSetRunsCurrentTemplate(ds)
	if ok {
		t.Fatal("an OnDelete DaemonSet with old pods must not be reported as running the current template")
	}

	if !strings.Contains(why, "delete pod -l k8s-app=cilium --field-selector spec.nodeName=<node>") {
		t.Errorf("message lacks the delete command: %s", why)
	}

	for _, want := range []string{"one node at a time", "get --raw /readyz"} {
		if !strings.Contains(why, want) {
			t.Errorf("message lacks %q: %s", want, why)
		}
	}

	if strings.Contains(why, "rollout restart") {
		t.Errorf("message names rollout restart, which restarts nothing on OnDelete: %s", why)
	}
}
