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

import "slices"

// RotationAbandonable reports whether deleting op may abandon a rotation: the controller then restores the
// state from before it (the new keys leave the bundle Secret, the lock is released, the machines are
// rendered from the old keys again). It is false for a ca-rotation or key-rotation that is past the point
// from which the nodes can be taken back, and true for every other operation and for a rotation that has not
// started or is done.
//
// The point of no return is the first stage in which a node, a pod or a stored object depends on the new
// material alone:
//
//   - ca-rotation: Accepting and AwaitingCredentials only add trust; Issuing makes the new CA sign, which
//     the Talos and aggregator CAs survive (the old one is still accepted), but not the Kubernetes CA
//     (pods have already been given the new one alone through kube-root-ca.crt) nor the etcd CA (a member
//     that went back to the old CA would not trust its neighbours' new certificates); Dropping and
//     Finalizing remove the old CA.
//   - key-rotation: only Accepting; from Issuing on the new secretbox key encrypts and the new
//     service-account key signs, so going back would leave Secrets and tokens nobody can read.
func RotationAbandonable(op *TalosOperation) bool {
	switch op.Spec.Kind {
	case TalosOperationKindCARotation:
		rs := op.Status.CARotation
		if rs == nil {
			return true
		}

		switch rs.Stage {
		case CARotationStageIssuing:
			return !slices.ContainsFunc(rs.Certificates, func(c CARotationCertificate) bool {
				return c.Name == CARotationTargetKubernetes || c.Name == CARotationTargetEtcd
			})
		case CARotationStageDropping, CARotationStageFinalizing:
			return false
		}
	case TalosOperationKindKeyRotation:
		if rs := op.Status.KeyRotation; rs != nil {
			switch rs.Stage {
			case KeyRotationStageIssuing, KeyRotationStageReencrypting, KeyRotationStageSettlingTokens,
				KeyRotationStageDropping, KeyRotationStageFinalizing:
				return false
			}
		}
	}

	return true
}
