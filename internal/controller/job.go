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
	"maps"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// The one-shot Jobs of the installer and the Cilium Helm run share this shape:
// one attempt, the installer image, a ServiceAccount of their own, a hardened pod.

const (
	nameLabel = "app.kubernetes.io/name"

	// jobTTL keeps a finished Job (and its pod log) for a day: long enough to read why a run
	// failed, short enough that Jobs do not pile up.
	jobTTL = 24 * 60 * 60
)

// jobOutcome reads a Job's terminal condition: done when it completed, failed when it did not.
func jobOutcome(job *batchv1.Job) (done bool, failed *batchv1.JobCondition) {
	for i, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}

		switch cond.Type {
		case batchv1.JobComplete:
			return true, nil
		case batchv1.JobFailed:
			return false, &job.Status.Conditions[i]
		}
	}

	return false, nil
}

// jobUID is the user and group of the nonroot distroless image the Jobs run (the images' USER 65532).
const jobUID int64 = 65532

// newJob builds the Job of one container that runs once (no retry: a failed run is looked at,
// not blindly repeated) under serviceAccount. app is the name label the network policies select
// the pod by (ADR 0100); deadline 0 sets no active deadline.
func newJob(
	ns, name, app, serviceAccount string, deadline int64, labels, annotations map[string]string, ctr corev1.Container,
) *batchv1.Job {
	ctr.SecurityContext = &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	// A runaway Job must not starve the node it lands on (ADR 0108); a caller's own resources win.
	if ctr.Resources.Limits == nil && ctr.Resources.Requests == nil {
		ctr.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		}
	}

	job := &batchv1.Job{
		Namespace: ns, Name: name, Annotations: annotations,
		Labels: map[string]string{nameLabel: app},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To[int32](0),
			TTLSecondsAfterFinished: ptr.To[int32](jobTTL),
			Template: corev1.PodTemplateSpec{
				Labels: map[string]string{nameLabel: app},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: serviceAccount,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						RunAsUser:      ptr.To(jobUID),
						RunAsGroup:     ptr.To(jobUID),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{ctr},
				},
			},
		},
	}

	maps.Copy(job.Labels, labels)

	if deadline > 0 {
		job.Spec.ActiveDeadlineSeconds = ptr.To(deadline)
	}

	return job
}
