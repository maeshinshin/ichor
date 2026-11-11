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

package cli

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWaitMachineReleasedReportsRebootRequiredWithoutWaiting(t *testing.T) {
	for name, tc := range map[string]struct {
		status metav1.ConditionStatus
		reason string
		reboot bool
	}{
		"became ready":    {metav1.ConditionTrue, testReadyReason, false},
		"reboot required": {metav1.ConditionFalse, reasonRebootRequired, true},
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestMachine("c")
			m.Status.Conditions = []metav1.Condition{{
				Type: conditionReady, Status: tc.status, Reason: tc.reason, LastTransitionTime: metav1.Now(),
			}}

			deps := adoptDeps{
				K8s:         fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(m).Build(),
				ReleaseWait: time.Minute, Interval: 5 * time.Millisecond,
			}

			start := time.Now()

			reboot, err := waitMachineReleased(context.Background(), deps, types.NamespacedName{Namespace: m.Namespace, Name: m.Name})
			if err != nil || reboot != tc.reboot || time.Since(start) > 10*time.Second {
				t.Fatalf("waitMachineReleased = %v, %v after %s; want %v, nil at once", reboot, err, time.Since(start), tc.reboot)
			}
		})
	}
}
