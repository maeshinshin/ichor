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

	"golang.org/x/mod/semver"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
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

	// IchorInstallerTargetAnnotation records the version a Job was started for, so a changed
	// spec.version is noticed instead of silently running the old installer to completion.
	IchorInstallerTargetAnnotation = "ichor.maesh.dev/installer-target-version"

	// ichorInstallerOperationLabel carries a hash of the operation's namespace/name; labels
	// cannot hold both, and the Job lives in the controller's namespace, not the operation's.
	ichorInstallerOperationLabel = "ichor.maesh.dev/installer-operation"
)

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

// NewIchorUpgradeAdvancer builds the executor for TalosOperation kind: ichor (ADR 0059). It
// launches the installer Job for IchorInstallation.spec.version and reports on it; the
// controller never replaces itself -- the Job, a separate process running the target
// version's image, does (design, "Upgrades").
//
// An Ichor upgrade has no machines, so the backbone calls the executor once per reconcile
// with a nil machine (advanceIchor). The Job is named by the operation, so every call looks
// at the same Job: the first creates it, later calls report its progress.
//
// Everything is re-derived per call (the ADR 0043 discipline): nothing is remembered in
// the process, so a restart of the controller -- which the Job itself causes -- resumes
// from the Job and the cluster.
func NewIchorUpgradeAdvancer(c client.Client, controllerNamespace string, noCiliumPolicies bool) AdvanceMachineFunc {
	return func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine, op *ichorv1alpha1.TalosOperation,
	) (bool, error) {
		var inst ichorv1alpha1.IchorInstallation
		if err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
			return false, fmt.Errorf("reading IchorInstallation %q: %w", ichorv1alpha1.IchorInstallationName, err)
		}

		// Done must be decided before the mode: after a successful upgrade the new
		// controller resumes this operation, and an installation that has become
		// external in the meantime is still an upgrade that happened.
		if inst.Spec.Version == version.Version {
			return true, nil
		}

		// Second line behind the webhook (ADR 0105): Helm moves this install, not the installer Job.
		if inst.HelmManaged() {
			return false, errors.New(ichorv1alpha1.IchorHelmManagedMessage)
		}

		if semver.IsValid(version.Version) && semver.Compare(inst.Spec.Version, version.Version) < 0 {
			return false, fmt.Errorf("spec.version %s is older than the running %s: Ichor does not downgrade",
				inst.Spec.Version, version.Version)
		}

		return advanceInstallerJob(ctx, c, controllerNamespace, op, &inst, detectedCNI(cluster), noCiliumPolicies)
	}
}

// advanceIchor steps an ichor operation: no machine is selected or recorded, the operation's
// progress is the installer Job's.
func (r *TalosOperationReconciler) advanceIchor(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	if err := lock.Renew(ctx, controllerIdentity); err != nil {
		return renewRefusal(ctx, err)
	}

	what := "the installer Job"
	if isCNIUpgrade(op) {
		what = "the Cilium chart upgrade"
	}

	done, err := r.Advance(ctx, cluster, nil, op)
	if err != nil {
		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationStepFailed, err.Error())
	}

	if done {
		if err := lock.Release(ctx, controllerIdentity); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseCompleted,
			reasonOperationComplete, what+" completed")
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseInProgress,
		reasonOperationAdvancing, what+" is running")
}

func advanceInstallerJob(
	ctx context.Context, c client.Client, ns string, op *ichorv1alpha1.TalosOperation, inst *ichorv1alpha1.IchorInstallation, cni string, noPolicies bool,
) (bool, error) {
	name := ichorInstallerJobName(op)

	var job batchv1.Job

	err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &job)

	switch {
	case apierrors.IsNotFound(err):
		return false, c.Create(ctx, newInstallerJob(ns, name, op, inst, cni, noPolicies))
	case err != nil:
		return false, err
	}

	if got := job.Annotations[IchorInstallerTargetAnnotation]; got != inst.Spec.Version {
		return false, fmt.Errorf("installer Job %s was started for %s but spec.version is now %s; "+
			"delete the Job and this operation, then create a new operation", name, got, inst.Spec.Version)
	}

	done, failed := jobOutcome(&job)
	if failed != nil {
		return false, fmt.Errorf("installer Job %s failed (%s: %s); the installer rolls back from its snapshot "+
			"before it fails, see the Job's pod log", name, failed.Reason, failed.Message)
	}

	return done, nil
}

// newInstallerJob is the whole installer process: the target version's own image, run once,
// under a ServiceAccount that exists for this and nothing else.
func newInstallerJob(ns, name string, op *ichorv1alpha1.TalosOperation, inst *ichorv1alpha1.IchorInstallation, cni string, noPolicies bool) *batchv1.Job {
	sum := sha256.Sum256([]byte(op.Namespace + "/" + op.Name))

	// One attempt: a retry after a rollback would redo a failed upgrade without anyone having
	// looked at why. The operation pauses instead. installerDeadline bounds the run in time (ADR 0108).
	args := []string{"installer", "run", "--namespace=" + ns, "--run-id=" + name, "--cni-detected=" + cni}
	if noPolicies {
		args = append(args, "--no-cilium-policies")
	}

	return newJob(ns, name, "ichor-installer", IchorInstallerServiceAccount, installerDeadline,
		map[string]string{ichorInstallerOperationLabel: hex.EncodeToString(sum[:])[:32]},
		map[string]string{IchorInstallerTargetAnnotation: inst.Spec.Version},
		corev1.Container{
			Name:  "installer",
			Image: inst.Spec.InstallerRepository + ":" + inst.Spec.Version,
			Args:  args,
		})
}
