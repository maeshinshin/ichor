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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// The Cilium Helm Job (ADR 0099, decision 2). The controller never runs Helm: running it needs
// what cluster-admin has, so a one-shot Job under its own ServiceAccount does, from the
// installer's image (the hidden subcommand `ichor cni helm apply`). Names the Job and its
// RBAC share (config/rbac/ichor-cni, the chart's rbac/ichor-cni.yaml).
const (
	// CNIServiceAccount is the Job's ServiceAccount.
	CNIServiceAccount = "ichor-cni"

	// cniHelmPodLabel is what config/cilium-network-policy and the chart's policy select the
	// Job's pod by (ADR 0100); it differs from the installer's so the two Jobs get their own
	// egress rules.
	cniHelmPodLabel = "ichor-cni"

	cniHelmJobPrefix = "ichor-cni-"

	// cniJobNamespace is where the Job (and its ServiceAccount) run: kube-system, where Cilium
	// itself lives. The Job needs hostNetwork (below), which Talos's default Pod Security
	// admission config (baseline enforce, exemptions for kube-system) refuses in ichor-system;
	// kube-system is the exempted namespace. The release record Secret stays in the controller's
	// namespace (the storage namespace the callers pass).
	cniJobNamespace = cilium.ReleaseNamespace

	// CNIValuesHashAnnotation records the effective-values hash a Job was started for, so a
	// finished Job is only taken as proof for the values it ran with.
	CNIValuesHashAnnotation = "ichor.maesh.dev/cni-values-hash"

	// cniClusterLabel carries a hash of the TalosCluster's namespace/name (labels cannot hold
	// both, and the Job lives in the controller's namespace, not the cluster's).
	cniClusterLabel = "ichor.maesh.dev/cni-cluster"

	// cniHelmDeadline bounds one run: the in-Job wait for the rollout is 10 minutes
	// (cilium.DefaultApplyTimeout), plus a rollback of the same size when an upgrade fails.
	cniHelmDeadline = 25 * 60

	// kubePrismHost and kubePrismPort are where the Job reaches the API server: the node's own
	// KubePrism endpoint, which keeps working while Cilium restarts (replaced mode requires
	// KubePrism on 7445 already, ADR 0099).
	kubePrismHost = "localhost"
	kubePrismPort = "7445"
)

// cniPodGrace is how long a Job may exist without a pod before it is taken to be stuck (its pods
// are refused, e.g. by admission) instead of waiting for the 25-minute deadline. A variable so a
// test can shorten it.
var cniPodGrace = 2 * time.Minute

// CNIValuesHash is a stable hash of the effective Cilium values cluster stores; the Job for
// one value set has one name, and a changed value is a new Job. Hooks for the hand-over
// (unit 8: "deployed at the effective-values hash") and the kube-proxy switch (unit 9).
func CNIValuesHash(cluster *ichorv1alpha1.TalosCluster) (string, error) {
	vals, err := cilium.EffectiveValuesFor(cluster)
	if err != nil {
		return "", err
	}

	return hashCiliumValues(vals)
}

// hashCiliumValues hashes a values map; the same function reads a release record's config back
// so the two sides compare.
func hashCiliumValues(vals map[string]any) (string, error) {
	raw, err := json.Marshal(vals) // map keys marshal sorted: the bytes are stable
	if err != nil {
		return "", fmt.Errorf("hashing effective Cilium values: %w", err)
	}

	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:]), nil
}

// CNIHelmJobFailedError is what AdvanceCNIHelmJob returns for a Job that ended Failed, so a caller
// can tell it from a transient API error and look up the Job's pod log.
type CNIHelmJobFailedError struct {
	JobName, Reason, Message string

	// NoPod marks a Job that never got a pod (see cniPodGrace); AdvanceCNIHelmJob has deleted
	// it, so the next call starts a new one. The reason is in the Job's FailedCreate events.
	NoPod bool
}

func (e *CNIHelmJobFailedError) Error() string {
	if e.NoPod {
		return fmt.Sprintf("cilium Helm Job %s created no pod within %s: %s", e.JobName, cniPodGrace, e.Message)
	}

	return fmt.Sprintf("cilium Helm Job %s failed (%s: %s); an upgrade rolled back before it failed, "+
		"see the Job's pod log", e.JobName, e.Reason, e.Message)
}

func cniHelmClusterHash(cluster *ichorv1alpha1.TalosCluster) string {
	sum := sha256.Sum256([]byte(cluster.Namespace + "/" + cluster.Name))

	return hex.EncodeToString(sum[:])[:32]
}

// cniHelmJobName derives the Job's name from the cluster and the values hash, so the same
// request always finds the same Job (the caller is re-entered every reconcile).
func cniHelmJobName(cluster *ichorv1alpha1.TalosCluster, valuesHash string) string {
	sum := sha256.Sum256([]byte(cluster.Namespace + "/" + cluster.Name + "@" + valuesHash))

	return cniHelmJobPrefix + hex.EncodeToString(sum[:])[:10]
}

// AdvanceCNIHelmJob makes sure the Cilium Helm Job for cluster's current effective values
// exists in kube-system (ns is where the release record is stored) and reports whether it has
// completed. A failed Job is an error carrying its reason; the Job's pod log has the detail. A
// Job that got no pod within cniPodGrace is deleted and reported the same way (NoPod). It is the one entry the hand-over (unit 8) and
// the kube-proxy switch (unit 9) call; taking the cluster operation lock around it is theirs
// (the switch and a values edit must not overlap).
func AdvanceCNIHelmJob(
	ctx context.Context, c client.Client, ns string, cluster *ichorv1alpha1.TalosCluster, inst *ichorv1alpha1.IchorInstallation,
) (bool, error) {
	return advanceCNIHelmJob(ctx, c, ns, cluster, inst, "")
}

// advanceCNIHelmJob is AdvanceCNIHelmJob with a salt in the Job name: an upgrade operation needs a
// Job of its own even when a finished one for the same values exists (the hand-over left one).
func advanceCNIHelmJob(
	ctx context.Context, c client.Client, ns string, cluster *ichorv1alpha1.TalosCluster, inst *ichorv1alpha1.IchorInstallation, salt string,
) (bool, error) {
	hash, err := CNIValuesHash(cluster)
	if err != nil {
		return false, err
	}

	name := cniHelmJobName(cluster, hash+salt)

	var job batchv1.Job

	err = c.Get(ctx, types.NamespacedName{Namespace: cniJobNamespace, Name: name}, &job)

	switch {
	case apierrors.IsNotFound(err):
		return false, c.Create(ctx, newCNIHelmJob(ns, name, cluster, inst, hash))
	case err != nil:
		return false, err
	}

	done, failed := jobOutcome(&job)
	if failed != nil {
		return false, &CNIHelmJobFailedError{JobName: name, Reason: failed.Reason, Message: failed.Message}
	}

	// A Job whose pods cannot be created (admission refuses them) would otherwise sit until its
	// deadline, holding the caller's cluster lock. No pod after the grace period: remove the Job
	// and report it; the caller releases the lock and the next pass starts a fresh Job.
	if !done && job.Status.Active == 0 && !job.CreationTimestamp.IsZero() && time.Since(job.CreationTimestamp.Time) > cniPodGrace {
		if err := c.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			return false, err
		}

		return false, &CNIHelmJobFailedError{JobName: name, Reason: "PodNotCreated", NoPod: true,
			Message: "see the Job's FailedCreate events in " + cniJobNamespace}
	}

	return done, nil
}

// newCNIHelmJob is the whole Cilium Helm process (in kube-system, storing its release record in
// storageNS): the installer's image, run once, under a
// ServiceAccount that exists for this and nothing else, on the node's network so it keeps
// its API access while Cilium restarts.
func newCNIHelmJob(
	storageNS, name string, cluster *ichorv1alpha1.TalosCluster, inst *ichorv1alpha1.IchorInstallation, valuesHash string,
) *batchv1.Job {
	// One attempt, as for the installer: an upgrade that failed has been rolled back, and
	// running it again without anyone looking at why would only repeat it. The caller
	// reports the failure and starts a new run when asked to.
	job := newJob(cniJobNamespace, name, cniHelmPodLabel, CNIServiceAccount, cniHelmDeadline,
		map[string]string{cniClusterLabel: cniHelmClusterHash(cluster)},
		map[string]string{CNIValuesHashAnnotation: valuesHash},
		corev1.Container{
			Name:  "cni",
			Image: inst.Spec.InstallerRepository + ":" + inst.Spec.Version,
			Args: []string{
				"cni", "helm", "apply",
				"--cluster=" + cluster.Name,
				"--namespace=" + cluster.Namespace,
				"--storage-namespace=" + storageNS,
			},
			Env: []corev1.EnvVar{
				{Name: "KUBERNETES_SERVICE_HOST", Value: kubePrismHost},
				{Name: "KUBERNETES_SERVICE_PORT", Value: kubePrismPort},
				// Discovery's cache lands in the emptyDir below, not in a read-only $HOME.
				{Name: "HOME", Value: "/tmp"},
			},
			VolumeMounts: []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}},
		})

	// The pod has to run where the pod network does not work yet or is being replaced: on the
	// host network, tolerating every taint (a not-ready node included), and talking to the API
	// server through KubePrism.
	pod := &job.Spec.Template.Spec
	pod.HostNetwork = true
	pod.DNSPolicy = corev1.DNSClusterFirstWithHostNet
	pod.Tolerations = []corev1.Toleration{{Operator: corev1.TolerationOpExists}}
	pod.Volumes = []corev1.Volume{{Name: "tmp", EmptyDir: &corev1.EmptyDirVolumeSource{}}}

	return job
}
