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

import "testing"

func TestRotationAbandonable(t *testing.T) {
	caOp := func(phase, stage string, targets ...string) *TalosOperation {
		op := &TalosOperation{Spec: TalosOperationSpec{Kind: TalosOperationKindCARotation}}
		op.Status.Phase = phase

		if stage != "" {
			op.Status.CARotation = &TalosOperationCARotationStatus{Stage: stage}
			for _, n := range targets {
				op.Status.CARotation.Certificates = append(op.Status.CARotation.Certificates, CARotationCertificate{Name: n})
			}
		}

		return op
	}

	keyOp := func(stage string) *TalosOperation {
		op := &TalosOperation{Spec: TalosOperationSpec{Kind: TalosOperationKindKeyRotation}}
		op.Status.Phase = TalosOperationPhasePaused
		op.Status.KeyRotation = &TalosOperationKeyRotationStatus{Stage: stage}

		return op
	}

	both := []string{CARotationTargetTalos, CARotationTargetKubernetes}

	for name, tc := range map[string]struct {
		op   *TalosOperation
		want bool
	}{
		"ca accepting":                      {caOp(TalosOperationPhasePaused, CARotationStageAccepting, both...), true},
		"ca awaiting credentials":           {caOp(TalosOperationPhasePaused, CARotationStageAwaitingCredentials, both...), true},
		"ca issuing of the Talos CA alone":  {caOp(TalosOperationPhasePaused, CARotationStageIssuing, CARotationTargetTalos), true},
		"ca issuing of the aggregator CA":   {caOp(TalosOperationPhasePaused, CARotationStageIssuing, CARotationTargetAggregator), true},
		"ca issuing with the Kubernetes CA": {caOp(TalosOperationPhasePaused, CARotationStageIssuing, both...), false},
		"ca issuing with the etcd CA":       {caOp(TalosOperationPhasePaused, CARotationStageIssuing, CARotationTargetEtcd), false},
		"ca dropping":                       {caOp(TalosOperationPhasePaused, CARotationStageDropping, both...), false},
		"ca finalizing":                     {caOp(TalosOperationPhaseInProgress, CARotationStageFinalizing, both...), false},
		"ca done":                           {caOp(TalosOperationPhaseCompleted, CARotationStageDone, both...), true},
		"ca not started":                    {caOp(TalosOperationPhasePending, ""), true},
		"key accepting":                     {keyOp(KeyRotationStageAccepting), true},
		"key issuing":                       {keyOp(KeyRotationStageIssuing), false},
		"key reencrypting":                  {keyOp(KeyRotationStageReencrypting), false},
		"key dropping":                      {keyOp(KeyRotationStageDropping), false},
		"another kind":                      {&TalosOperation{Spec: TalosOperationSpec{Kind: TalosOperationKindNode}}, true},
	} {
		if got := RotationAbandonable(tc.op); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
