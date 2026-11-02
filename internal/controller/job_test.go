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
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// Every Job pod names its user and group and bounds its resources.
func TestNewJobPinsTheUserAndBoundsResources(t *testing.T) {
	job := newJob("ns", "j", "app", "sa", 0, nil, nil, corev1.Container{Name: "c", Image: "i"})
	sc := job.Spec.Template.Spec.SecurityContext

	if sc.RunAsUser == nil || *sc.RunAsUser != jobUID || sc.RunAsGroup == nil || *sc.RunAsGroup != jobUID {
		t.Fatalf("the pod does not run as %d:%d: %+v", jobUID, jobUID, sc)
	}

	res := job.Spec.Template.Spec.Containers[0].Resources
	if res.Limits.Memory().IsZero() || res.Limits.Cpu().IsZero() || res.Requests.Memory().IsZero() {
		t.Fatalf("the container has no resource requests and limits: %+v", res)
	}
}
