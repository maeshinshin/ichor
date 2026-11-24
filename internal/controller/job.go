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
	"context"
	"fmt"
	"maps"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The one-shot Jobs of the installer and the Cilium Helm run share this shape:
// one attempt, the installer image, a ServiceAccount of their own, a hardened pod.

const (
	nameLabel = "app.kubernetes.io/name"

	// jobNameLabel is the label the Job controller puts on a Job's pods. The unprefixed "job-name" is deprecated since Kubernetes 1.27.
	jobNameLabel = "batch.kubernetes.io/job-name"

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

// jobMessageLimit is how long a container's termination message may be: the kubelet cuts a longer
// one to this many bytes (Container.terminationMessagePath in the Kubernetes API reference).
const jobMessageLimit = 4096

// JobTerminationMessage is the termination message a Job's container wrote, as the kubelet copied it into
// the pod's status: that of the container that finished last among the Job's pods and wrote one, "" when
// none did. The Job's backoffLimit is 0, so there is one pod unless a pod was lost before it failed. A
// message at jobMessageLimit may have been cut, and says so. pods must be uncached: the controller's
// cache holds no pods (it has no watch on them).
func JobTerminationMessage(ctx context.Context, pods client.Reader, ns, jobName string) (string, error) {
	var list corev1.PodList
	if err := pods.List(ctx, &list, client.InNamespace(ns), client.MatchingLabels{jobNameLabel: jobName}); err != nil {
		return "", fmt.Errorf("listing the pods of Job %s: %w", jobName, err)
	}

	var msg string
	var finished time.Time

	for i := range list.Items {
		for _, cs := range list.Items[i].Status.ContainerStatuses {
			t := cs.State.Terminated
			if t == nil || t.Message == "" || (msg != "" && !t.FinishedAt.After(finished)) {
				continue
			}

			msg, finished = t.Message, t.FinishedAt.Time
		}
	}

	if len(msg) >= jobMessageLimit {
		msg += " (at the termination message limit, so it may be cut off)"
	}

	return msg, nil
}

// jobUID is the user and group of the nonroot distroless image the Jobs run (the images' USER 65532).
const jobUID int64 = 65532

// newJob builds the Job of one container that runs once (no retry: a failed run is looked at,
// not blindly repeated) under serviceAccount. app is the name label the network policies select
// the pod by; deadline 0 sets no active deadline.
func newJob(
	ns, name, app, serviceAccount string, deadline int64, labels, annotations map[string]string, ctr corev1.Container,
) *batchv1.Job {
	ctr.SecurityContext = &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	// A runaway Job must not starve the node it lands on; a caller's own resources win.
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
