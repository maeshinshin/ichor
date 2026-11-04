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
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// The Cilium Helm Job. The controller never runs Helm: running it needs
// what cluster-admin has, so a one-shot Job under its own ServiceAccount does, from the
// installer's image (the hidden subcommand `ichor cni helm apply`). Names the Job and its
// RBAC share (config/rbac/ichor-cni, the chart's rbac/ichor-cni.yaml).
const (
	// CNIServiceAccount is the Job's ServiceAccount.
	CNIServiceAccount = "ichor-cni"

	// cniHelmPodLabel is what config/cilium-network-policy and the chart's policy select the
	// Job's pod by; it differs from the installer's so the two Jobs get their own
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
	// KubePrism on 7445 already).
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
//
// mode is the kube-proxy mode Cilium is to run with. The hand-over passes the mode the cluster
// runs now (currentKubeProxyMode), never spec.cni.kubeProxy: only the kube-proxy switch applies
// the spec's mode, under its operation.
//
// nodes is how many Nodes the cluster has now (clusterNodeCount; 0 unknown): the operator's replicas follow
// it, so a node joining can change the values. The Job is handed the same
// number (--nodes), so it applies the values that were hashed.
func CNIValuesHash(cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, nodes int) (string, error) {
	vals, err := cilium.EffectiveValuesForNodes(cluster, mode, nodes)
	if err != nil {
		return "", err
	}

	return hashCiliumValues(vals)
}

// clusterNodeCount is how many Nodes the cluster has right now, every one of them: a Node that is not
// Ready yet (the first, before Cilium is up) still counts, and the operator tolerates every taint. It is
// the input of CNIValuesHash, so a caller counts once and passes the number to everything that hashes or
// applies the values.
func clusterNodeCount(ctx context.Context, reader client.Reader) (int, error) {
	var nodes metav1.PartialObjectMetadataList

	nodes.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("NodeList"))

	if err := reader.List(ctx, &nodes); err != nil {
		return 0, fmt.Errorf("counting the cluster's nodes for the Cilium values: %w", err)
	}

	return len(nodes.Items), nil
}

// currentKubeProxyMode is the kube-proxy mode Cilium runs with now, which is what every Cilium
// Helm Job outside a kube-proxy switch must reproduce: cilium-config's kube-proxy-replacement. The
// switch's own Job is the only thing that changes it, and in the enabling direction it changes only
// after kube-proxy is back, so it is the one signal that is never ahead of the nodes (status.cni.
// kubeProxy is set first when enabling). Before Cilium has a cilium-config: status.cni.kubeProxy,
// then spec.cni.kubeProxy (nothing has been switched yet, so the spec is what the bootstrap rendered).
func currentKubeProxyMode(ctx context.Context, reader client.Reader, cluster *ichorv1alpha1.TalosCluster) (cni.KubeProxyMode, error) {
	var cm corev1.ConfigMap

	err := reader.Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: ciliumConfigName}, &cm)

	switch {
	case err == nil:
		if v, ok := cm.Data[ciliumKPRKey]; ok {
			if ciliumReplaces(v) {
				return cni.KubeProxyReplaced, nil
			}

			return cni.KubeProxyEnabled, nil
		}
	case !apierrors.IsNotFound(err):
		return "", fmt.Errorf("reading cilium-config for the kube-proxy mode Cilium runs: %w", err)
	}

	mode, _ := render.KubeProxyMode(cluster)
	if mode == "" {
		mode = cni.KubeProxyEnabled
	}

	return mode, nil
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
// Job that got no pod within cniPodGrace is deleted and reported the same way (NoPod). It is the one entry the hand-over (unit 8),
// the kube-proxy switch (unit 9) and the upgrade call; taking the cluster operation lock around it is theirs
// (the switch and a values edit must not overlap). A salt in the Job name gives an upgrade
// operation a Job of its own even when a finished one for the same values exists.
//
// A caller that needs a run of its own passes since: the Job name is a hash of the values, so a Job
// that ended before since (the install's, or an earlier switch's, within its 24 h TTL) is not its
// run. It is deleted and the next call starts a new one. A zero since takes any Job as it is.
func AdvanceCNIHelmJob(
	ctx context.Context, c client.Client, ns string, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, nodes int,
	inst *ichorv1alpha1.IchorInstallation, salt string, since time.Time,
) (bool, error) {
	if mode == "" {
		mode = cni.KubeProxyEnabled
	}

	hash, err := CNIValuesHash(cluster, mode, nodes)
	if err != nil {
		return false, err
	}

	name := cniHelmJobName(cluster, hash+salt)

	var job batchv1.Job

	err = c.Get(ctx, types.NamespacedName{Namespace: cniJobNamespace, Name: name}, &job)

	switch {
	case apierrors.IsNotFound(err):
		// AlreadyExists: the cache has not seen the Job just created.
		return false, client.IgnoreAlreadyExists(c.Create(ctx, newCNIHelmJob(ns, name, cluster, mode, inst, hash, takeoverJob(cluster), nodes)))
	case err != nil:
		return false, err
	}

	done, failed := jobOutcome(&job)
	if ended := jobEnded(&job); !ended.IsZero() && ended.Before(since) {
		return false, deleteCNIHelmJob(ctx, c, &job)
	}

	if failed != nil {
		return false, &CNIHelmJobFailedError{JobName: name, Reason: failed.Reason, Message: failed.Message}
	}

	// A Job whose pods cannot be created (admission refuses them) would otherwise sit until its
	// deadline, holding the caller's cluster lock. No pod after the grace period: remove the Job
	// and report it; the caller releases the lock and the next pass starts a fresh Job.
	if !done && job.Status.Active == 0 && !job.CreationTimestamp.IsZero() && time.Since(job.CreationTimestamp.Time) > cniPodGrace {
		if err := deleteCNIHelmJob(ctx, c, &job); err != nil {
			return false, err
		}

		return false, &CNIHelmJobFailedError{JobName: name, Reason: "PodNotCreated", NoPod: true,
			Message: "see the Job's FailedCreate events in " + cniJobNamespace}
	}

	return done, nil
}

// deleteCNIHelmJob removes a Helm Job and its pods; a Job already gone is fine.
func deleteCNIHelmJob(ctx context.Context, c client.Client, job *batchv1.Job) error {
	return client.IgnoreNotFound(c.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)))
}

// jobEnded is when a finished Job ended, or the zero time.
func jobEnded(job *batchv1.Job) time.Time {
	if job.Status.CompletionTime != nil {
		return job.Status.CompletionTime.Time
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return cond.LastTransitionTime.Time
		}
	}

	return time.Time{}
}

// takeoverJob is whether the Job takes over a Cilium someone else installed: every Job
// for an adopted cluster until the hand-over has completed, so a retry after a failed first run
// still retires the foreign Helm record (the retirement is idempotent). No Job of a cluster
// Ichor bootstrapped, and none after the hand-over, takes over anything.
func takeoverJob(cluster *ichorv1alpha1.TalosCluster) bool {
	return cluster.Annotations[ichorv1alpha1.TalosClusterAdoptedAnnotation] == strconv.FormatBool(true) &&
		!apimeta.IsStatusConditionTrue(cluster.Status.Conditions, render.ConditionCNIHandover)
}

// newCNIHelmJob is the whole Cilium Helm process (in kube-system, storing its release record in
// storageNS): the installer's image, run once, under a
// ServiceAccount that exists for this and nothing else, on the node's network so it keeps
// its API access while Cilium restarts.
func newCNIHelmJob(
	storageNS, name string, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode,
	inst *ichorv1alpha1.IchorInstallation, valuesHash string, takeover bool, nodes int,
) *batchv1.Job {
	// One attempt, as for the installer: an upgrade that failed has been rolled back, and
	// running it again without anyone looking at why would only repeat it. The caller
	// reports the failure and starts a new run when asked to.
	args := []string{
		"cni", "helm", "apply",
		"--cluster=" + cluster.Name,
		"--namespace=" + cluster.Namespace,
		"--kube-proxy=" + string(mode),
		"--storage-namespace=" + storageNS,
		// Safe: the callers hold the cluster's operation lock, so at most one such Job per cluster
		// runs at a time, and the release record is Helm's own lock. A pending-* record left by a
		// killed run is therefore stale and Apply may repair it at once, not after its timeout.
		"--exclusive",
	}
	if takeover {
		args = append(args, "--takeover")
	}

	// The node count the values were hashed with (see CNIValuesHash).
	if nodes > 0 {
		args = append(args, "--nodes="+strconv.Itoa(nodes))
	}

	job := newJob(cniJobNamespace, name, cniHelmPodLabel, CNIServiceAccount, cniHelmDeadline,
		map[string]string{cniClusterLabel: cniHelmClusterHash(cluster)},
		map[string]string{CNIValuesHashAnnotation: valuesHash},
		corev1.Container{
			Name:  "cni",
			Image: cniJobImage(inst),
			Args:  args,
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

// cniJobImage is the installer image the Cilium Helm Job runs: the digest the last Ichor upgrade pinned (and
// verified, or recorded as skipped) when that record is for the installed version and repository, so this Job
// runs the very image the installer did and not whatever the tag points at now. Without
// such a record (a fresh install, a Helm-only install, a hand-edited version) it is the tag, as the apiserver's
// Job-image policy also allows.
func cniJobImage(inst *ichorv1alpha1.IchorInstallation) string {
	if rec := inst.Status.InstallerImage; rec != nil && rec.Digest != "" &&
		rec.Version == inst.Spec.Version && rec.Repository == inst.Spec.InstallerRepository {
		return inst.Spec.InstallerRepository + "@" + rec.Digest
	}

	return inst.Spec.InstallerRepository + ":" + inst.Spec.Version
}

// defaultInstallerRepository is the API default of IchorInstallation.spec.installerRepository.
const defaultInstallerRepository = "ghcr.io/maeshinshin/ichor-installer"

// installerSource is the IchorInstallation whose installer image the Cilium Helm Job runs. A
// Helm-installed Ichor has no such object, so the controller's own version and the
// default repository stand in for it.
func installerSource(ctx context.Context, r client.Reader) (*ichorv1alpha1.IchorInstallation, error) {
	var inst ichorv1alpha1.IchorInstallation

	err := r.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst)

	switch {
	case err == nil:
		return &inst, nil
	case apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err):
		return &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{
			Version: version.Version, InstallerRepository: defaultInstallerRepository,
		}}, nil
	default:
		return nil, fmt.Errorf("reading IchorInstallation %q for the Cilium Helm Job: %w", ichorv1alpha1.IchorInstallationName, err)
	}
}
