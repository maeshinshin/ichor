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
	"errors"
	"fmt"
	"time"

	"golang.org/x/mod/semver"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/imagesig"
	"github.com/maeshinshin/ichor/internal/oidctrust"
	"github.com/maeshinshin/ichor/internal/version"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

// Names the installer Job and its RBAC share (config/rbac/ichor-installer, the chart's
// rbac/ichor-installer-*.yaml).
const (
	// IchorInstallerServiceAccount is the Job's ServiceAccount.
	IchorInstallerServiceAccount = "ichor-installer"

	// installerDeadline is the longest an installer Job may run, in seconds: a post-check and a rollback
	// are five minutes each per step, so half an hour is generous and still finite.
	installerDeadline = 30 * 60

	ichorInstallerJobPrefix = "ichor-installer-"

	// controlPlaneLabel names the controller Deployment, with valueControllerMgr.
	controlPlaneLabel = "control-plane"

	// IchorInstallerTargetAnnotation records the version a Job was started for, so a changed
	// spec.version is noticed instead of silently running the old installer to completion.
	IchorInstallerTargetAnnotation = "ichor.maesh.dev/installer-target-version"

	// ichorInstallerOperationLabel carries a hash of the operation's namespace/name; labels
	// cannot hold both, and the Job lives in the controller's namespace, not the operation's.
	ichorInstallerOperationLabel = "ichor.maesh.dev/installer-operation"

	// IchorInstallerDigestAnnotation records the image digest a Job runs.
	IchorInstallerDigestAnnotation = "ichor.maesh.dev/installer-image-digest"

	// installerImageTimeout bounds the registry requests that resolve and verify the installer image.
	installerImageTimeout = 2 * time.Minute

	// Reasons an Ichor upgrade pauses before it creates the installer Job.
	reasonInstallerImageUnresolved = "InstallerImageUnresolved"
	reasonInstallerImageUnverified = "InstallerImageUnverified"

	// reasonInstallerSignatureSkipped is the Reason of the Warning Event for a Job run without a signature check.
	reasonInstallerSignatureSkipped = "InstallerSignatureSkipped"
)

// InstallerImages pins the installer image to a digest and checks its signature.
// *imagesig.Sigstore is the production implementation.
type InstallerImages interface {
	// Resolve is the digest the tag points at now.
	Resolve(ctx context.Context, repository, tag string) (string, error)
	// Verify succeeds when a cosign signature of the digest was made by the identity.
	Verify(ctx context.Context, img imagesig.Image, id imagesig.Identity) error
}

// installerImageError is a refusal to create the Job. reason is the reason the operation pauses with.
type installerImageError struct {
	reason string
	err    error
}

func (e *installerImageError) Error() string { return e.err.Error() }
func (e *installerImageError) Unwrap() error { return e.err }

// pauseReason is the reason a failed step pauses its operation with.
func pauseReason(err error) string {
	if ie := (*installerImageError)(nil); errors.As(err, &ie) {
		return ie.reason
	}

	return reasonOperationStepFailed
}

// The operation does not need the installer Job after jobTTL: once the upgrade succeeded the new
// controller's version equals spec.version, which the executor checks before it looks for a Job.
// (If an expired failed Job is resumed, the executor starts a fresh attempt; the installer
// reuses the run's snapshot, so that is a retry, not a second snapshot.)

// ichorInstallerJobName derives the Job's name from the operation, so the same operation
// always finds the same Job (the executor is re-entered every reconcile).
func ichorInstallerJobName(op *ichorv1alpha1.TalosOperation) string {
	sum := sha256.Sum256([]byte(op.Namespace + "/" + op.Name))

	return ichorInstallerJobPrefix + hex.EncodeToString(sum[:])[:10]
}

// +kubebuilder:rbac:groups=batch,namespace=ichor-system,resources=jobs,verbs=get;list;watch;create

// NewIchorUpgradeAdvancer builds the executor for TalosOperation kind: ichor. It
// launches the installer Job for IchorInstallation.spec.version and reports on it; the
// controller never replaces itself -- the Job, a separate process running the target
// version's image, does (design, "Upgrades").
//
// An Ichor upgrade has no machines, so the backbone calls the executor once per reconcile
// with a nil machine (advanceIchor). The Job is named by the operation, so every call looks
// at the same Job: the first creates it, later calls report its progress.
//
// Everything is re-derived per call: nothing is remembered in
// the process, so a restart of the controller -- which the Job itself causes -- resumes
// from the Job and the cluster.
func NewIchorUpgradeAdvancer(
	c client.Client, uncached client.Reader, controllerNamespace string, noCiliumPolicies bool,
	images InstallerImages, recorder record.EventRecorder,
) AdvanceMachineFunc {
	return func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
	) (bool, error) {
		var inst ichorv1alpha1.IchorInstallation
		if err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
			return false, fmt.Errorf("reading IchorInstallation %q: %w", ichorv1alpha1.IchorInstallationName, err)
		}

		// Decided before the mode: after a successful upgrade the new controller resumes this
		// operation, and an installation that has become external in the meantime is still an
		// upgrade that happened. The new controller runs well before the upgrade is over (the
		// installer Job is still post-checking, ichor-ui still rolling), so a matching version
		// only means the Job and the rollout are what is left to wait for.
		if inst.Spec.Version == version.Version {
			return installerSettled(ctx, c, uncached, controllerNamespace, op)
		}

		// Second line behind the webhook: Helm moves this install, not the installer Job.
		if inst.HelmManaged() {
			return false, errors.New(ichorv1alpha1.IchorHelmManagedMessage)
		}

		if semver.IsValid(version.Version) && semver.Compare(inst.Spec.Version, version.Version) < 0 {
			return false, fmt.Errorf("spec.version %s is older than the running %s: Ichor does not downgrade",
				inst.Spec.Version, version.Version)
		}

		return advanceInstallerJob(ctx, c, uncached, controllerNamespace, op, &inst, detectedCNI(cluster), noCiliumPolicies, images, recorder)
	}
}

// advanceIchor steps an ichor operation: no machine is selected or recorded, the operation's
// progress is the installer Job's.
func (r *TalosOperationReconciler) advanceIchor(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	if err := lock.Renew(ctx, operationLockIdentity(op)); err != nil {
		return renewRefusal(ctx, err)
	}

	what := "the installer Job"
	if isCNIUpgrade(op) {
		what = "the Cilium chart upgrade"
	}

	done, err := r.Advance(ctx, cluster, nil, op)
	if err != nil {
		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, pauseReason(err), err.Error())
	}

	if done {
		if err := lock.Release(ctx, operationLockIdentity(op)); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseCompleted,
			reasonOperationComplete, what+" completed")
	}

	waiting := what + " is running"
	if !isCNIUpgrade(op) {
		if waiting, err = ichorWaitingFor(ctx, r.Client, r.APIReader, r.ControllerNamespace, op); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseInProgress,
		reasonOperationAdvancing, waiting)
}

// ichorWaitingFor says what an ichor operation that is not done is waiting for, for its status: the
// installer Job while it runs, then the first Deployment still rolling out. The executor reports only
// done or not, so the state is read again here.
func ichorWaitingFor(
	ctx context.Context, c client.Client, uncached client.Reader, ns string, op *ichorv1alpha1.TalosOperation,
) (string, error) {
	var job batchv1.Job

	switch err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: ichorInstallerJobName(op)}, &job); {
	case apierrors.IsNotFound(err): // not created yet, or long gone: the Deployments are what is left
	case err != nil:
		return "", err
	default:
		if done, _ := jobOutcome(&job); !done {
			return "the installer Job is running", nil
		}
	}

	waiting, err := ichorRolloutWaiting(ctx, uncached, ns)
	if err != nil {
		return "", err
	}

	if waiting != "" {
		return waiting, nil
	}

	return "finishing the upgrade", nil
}

func advanceInstallerJob(
	ctx context.Context, c client.Client, uncached client.Reader, ns string, op *ichorv1alpha1.TalosOperation,
	inst *ichorv1alpha1.IchorInstallation, cni string, noPolicies bool, images InstallerImages, recorder record.EventRecorder,
) (bool, error) {
	name := ichorInstallerJobName(op)

	var job batchv1.Job

	err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &job)

	switch {
	case apierrors.IsNotFound(err):
		img, err := pinInstallerImage(ctx, images, inst)
		if err != nil {
			return false, err
		}

		if err := recordInstallerImage(ctx, c, inst, img, recorder); err != nil {
			return false, err
		}

		return false, c.Create(ctx, newInstallerJob(ns, name, op, inst, img.Digest, cni, noPolicies))
	case err != nil:
		return false, err
	}

	if got := job.Annotations[IchorInstallerTargetAnnotation]; got != inst.Spec.Version {
		return false, fmt.Errorf("installer Job %s was started for %s but spec.version is now %s; "+
			"delete the Job and this operation, then create a new operation", name, got, inst.Spec.Version)
	}

	if got := job.Annotations[IchorInstallerDigestAnnotation]; inst.Spec.InstallerDigest != "" && got != inst.Spec.InstallerDigest {
		return false, fmt.Errorf("installer Job %s runs the image %s but spec.installerDigest is now %s; "+
			"delete the Job and this operation, then create a new operation", name, got, inst.Spec.InstallerDigest)
	}

	return installerJobSettled(ctx, uncached, ns, &job)
}

// pinnedInstallerImage is the installer image an Ichor upgrade runs and how far it was checked.
type pinnedInstallerImage struct {
	Digest       string
	Verification string // ichorv1alpha1.InstallerImageVerified or InstallerImageSkipped
	Signer       string
	Reason       string
}

// pinInstallerImage resolves the installer image to a digest and verifies its signature, or says why not. Nothing
// falls back to a tag or to an unchecked image: the only way past the signature is
// spec.installerSignature.mode skip, with its reason.
func pinInstallerImage(
	ctx context.Context, images InstallerImages, inst *ichorv1alpha1.IchorInstallation,
) (pinnedInstallerImage, error) {
	if images == nil {
		return pinnedInstallerImage{}, &installerImageError{reason: reasonInstallerImageUnverified,
			err: errors.New("this controller has no installer image verifier; refusing to run an unchecked installer image")}
	}

	ctx, cancel := context.WithTimeout(ctx, installerImageTimeout)
	defer cancel()

	repo, tag := inst.Spec.InstallerRepository, inst.Spec.Version
	digest := inst.Spec.InstallerDigest

	if digest == "" {
		var err error

		if digest, err = images.Resolve(ctx, repo, tag); err != nil {
			return pinnedInstallerImage{}, &installerImageError{reason: reasonInstallerImageUnresolved,
				err: fmt.Errorf("the installer image %s:%s was not resolved to a digest, so no installer Job was created "+
					"(check that the registry is reachable from the controller, or pin spec.installerDigest): %w", repo, tag, err)}
		}
	}

	if sig := inst.Spec.InstallerSignature; sig != nil && sig.Mode == ichorv1alpha1.InstallerSignatureSkip {
		return pinnedInstallerImage{Digest: digest, Verification: ichorv1alpha1.InstallerImageSkipped, Reason: sig.Reason}, nil
	}

	id := imagesig.IchorIdentity(tag)
	if err := images.Verify(ctx, imagesig.Image{Repository: repo, Digest: digest}, id); err != nil {
		return pinnedInstallerImage{}, &installerImageError{reason: reasonInstallerImageUnverified,
			err: fmt.Errorf("the installer image %s@%s is not verified, so no installer Job was created "+
				"(fix the registry or mirror, or run the upgrade with a recorded reason to skip the check: "+
				"`ichor upgrade ichor --skip-signature-check REASON`): %w", repo, digest, err)}
	}

	return pinnedInstallerImage{Digest: digest, Verification: ichorv1alpha1.InstallerImageVerified, Signer: id.SANRegexp}, nil
}

// recordInstallerImage writes what the Job will run to status.installerImage and, for a skipped check,
// a Warning Event: the opt-out is shown, never silent.
func recordInstallerImage(
	ctx context.Context, c client.Client, inst *ichorv1alpha1.IchorInstallation, img pinnedInstallerImage,
	recorder record.EventRecorder,
) error {
	orig := inst.DeepCopy()
	now := metav1.Now()

	inst.Status.InstallerImage = &ichorv1alpha1.IchorInstallerImageStatus{
		Version: inst.Spec.Version, Repository: inst.Spec.InstallerRepository, Digest: img.Digest,
		Verification: img.Verification, Signer: img.Signer, Reason: img.Reason, ResolvedAt: &now,
	}

	if err := c.Status().Patch(ctx, inst, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("recording the installer image in IchorInstallation status: %w", err)
	}

	if img.Verification == ichorv1alpha1.InstallerImageSkipped && recorder != nil {
		recorder.Eventf(inst, corev1.EventTypeWarning, reasonInstallerSignatureSkipped,
			"the signature of the installer image %s@%s for %s is NOT checked: %s",
			inst.Spec.InstallerRepository, img.Digest, inst.Spec.Version, img.Reason)
	}

	return nil
}

// installerSettled is the end of an upgrade once the running version is the target: the installer
// Job (if it still exists; it may be long gone) has succeeded and the Deployments have rolled out.
func installerSettled(
	ctx context.Context, c client.Client, uncached client.Reader, ns string, op *ichorv1alpha1.TalosOperation,
) (bool, error) {
	var job batchv1.Job

	switch err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: ichorInstallerJobName(op)}, &job); {
	case apierrors.IsNotFound(err):
		return ichorRolledOut(ctx, uncached, ns)
	case err != nil:
		return false, err
	}

	return installerJobSettled(ctx, uncached, ns, &job)
}

// installerJobSettled is done when the Job has succeeded and the controller and ichor-ui have
// finished rolling out; a failed Job is an error.
func installerJobSettled(ctx context.Context, uncached client.Reader, ns string, job *batchv1.Job) (bool, error) {
	done, failed := jobOutcome(job)
	if failed != nil {
		return false, fmt.Errorf("installer Job %s failed (%s: %s); the installer rolls back from its snapshot "+
			"before it fails, see the Job's pod log", job.Name, failed.Reason, failed.Message)
	}

	if !done {
		return false, nil
	}

	return ichorRolledOut(ctx, uncached, ns)
}

// ichorRolledOut reports whether the controller's and ichor-ui's Deployments have finished
// rolling out.
func ichorRolledOut(ctx context.Context, uncached client.Reader, ns string) (bool, error) {
	waiting, err := ichorRolloutWaiting(ctx, uncached, ns)
	if err != nil {
		return false, err
	}

	return waiting == "", nil
}

// ichorDeployments are Ichor's own workloads in its namespace, found by the labels every install path sets.
var ichorDeployments = []struct {
	role   string
	labels client.MatchingLabels
}{
	{"controller", client.MatchingLabels{controlPlaneLabel: valueControllerMgr}},
	{"ichor-ui", client.MatchingLabels{oidctrust.UIComponentLabel: oidctrust.UIComponentValue}},
}

// ichorRolloutWaiting names the first of the controller's and ichor-ui's Deployments that has not
// finished rolling out, as the operation's status says it; "" when both have. Both are found by the
// labels every install path sets (kustomize prefixes their names); one that is not installed is not
// waited for. The reader is uncached: a cached read of Deployments would start a cluster-wide
// informer (cache.go).
func ichorRolloutWaiting(ctx context.Context, uncached client.Reader, ns string) (string, error) {
	for _, dep := range ichorDeployments {
		var list appsv1.DeploymentList
		if err := uncached.List(ctx, &list, client.InNamespace(ns), dep.labels); err != nil {
			return "", fmt.Errorf("reading the Deployments %v: %w", dep.labels, err)
		}

		for i := range list.Items {
			d := &list.Items[i]
			if !deploymentRolledOut(d) {
				return fmt.Sprintf("waiting for the %s Deployment to finish rolling out (%d of %d updated replicas are available)",
					dep.role, min(d.Status.UpdatedReplicas, d.Status.AvailableReplicas), desiredReplicas(d)), nil
			}
		}
	}

	return "", nil
}

// desiredReplicas is the replica count a Deployment asks for: one when unset, as the API defaults it.
func desiredReplicas(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 1
	}

	return *d.Spec.Replicas
}

// deploymentRolledOut is kubectl rollout status's test: the controller has seen the latest spec,
// every replica is of that spec, no old replica is left, and all are ready and available.
func deploymentRolledOut(d *appsv1.Deployment) bool {
	want := desiredReplicas(d)

	s := d.Status

	return s.ObservedGeneration >= d.Generation &&
		s.UpdatedReplicas == want && s.Replicas == want && s.ReadyReplicas == want && s.AvailableReplicas == want
}

// newInstallerJob is the whole installer process: the target version's own image, pinned to digest, run
// once, under a ServiceAccount that exists for this and nothing else.
func newInstallerJob(
	ns, name string, op *ichorv1alpha1.TalosOperation, inst *ichorv1alpha1.IchorInstallation, digest, cni string, noPolicies bool,
) *batchv1.Job {
	sum := sha256.Sum256([]byte(op.Namespace + "/" + op.Name))

	// One attempt: a retry after a rollback would redo a failed upgrade without anyone having
	// looked at why. The operation pauses instead. installerDeadline bounds the run in time.
	args := []string{"installer", "run", "--namespace=" + ns, "--run-id=" + name, "--cni-detected=" + cni}
	if noPolicies {
		args = append(args, "--no-cilium-policies")
	}

	return newJob(ns, name, "ichor-installer", IchorInstallerServiceAccount, installerDeadline,
		map[string]string{ichorInstallerOperationLabel: hex.EncodeToString(sum[:])[:32]},
		map[string]string{IchorInstallerTargetAnnotation: inst.Spec.Version, IchorInstallerDigestAnnotation: digest},
		corev1.Container{
			Name:  "installer",
			Image: inst.Spec.InstallerRepository + "@" + digest,
			Args:  args,
		})
}
