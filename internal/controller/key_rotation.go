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

// This file implements the TalosOperationKindKeyRotation state machine: the replacement of the
// Kubernetes service-account signing key and the kube-apiserver secretbox key, in a dual-key period.
// TalosOperationReconciler dispatches here after taking the cluster lock.
//
// The stage lives in op.status.keyRotation and nowhere else; which control planes a stage still has to
// visit is re-derived from what each reports, so a controller killed at any point resumes from status.
//
// The new keys are written to the bundle Secret's keyrotation.yaml before any node is told to use them, so
// no crash can leave nodes on a key nobody holds. secrets.yaml is replaced, the retired keys recorded and
// keyrotation.yaml removed in one Secret update after the last pass is verified.
//
//	"" ............ preflight, save the new keys to keyrotation.yaml                       -> Accepting
//	Accepting ..... new service-account public key verifies, new secretbox key decrypts;
//	                the old keys still sign and encrypt                                    -> Issuing
//	Issuing ....... the new keys sign and encrypt; the old ones are still accepted         -> Reencrypting
//	Reencrypting .. every Secret is stored under the new secretbox key (a sweep that
//	                rewrites nothing is the proof)                                         -> SettlingTokens
//	SettlingTokens  the old service-account key is kept while bound tokens are re-issued
//	                and until no legacy token Secret depends on it                         -> Dropping
//	Dropping ...... the old keys are removed                                               -> Finalizing
//	Finalizing .... Secret replaced, retired keys recorded, BundleBackupStale raised,
//	                every control plane checked against the replaced bundle                -> Done
//
// Reencrypting is skipped without the secretbox target and SettlingTokens without the serviceAccount target.
//
// Control planes are applied one at a time and each is waited for until its kube-apiserver runs in a new
// container and answers /readyz, so the Kubernetes API stays up and no apiserver signs or encrypts with a
// key another cannot yet verify or decrypt. A step that cannot be verified never advances the stage: the
// operation is Paused with the reason and tried again every operationRequeueInterval.
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

const (
	// conditionKeyRotation is the rotation's own progress line: True while it advances, False while it is
	// blocked, with the reason.
	conditionKeyRotation = "KeyRotation"

	reasonOperationKeyRotating  = "ClusterKeyRotating"
	reasonKeyRotationInProgress = "KeyRotationInProgress"

	reasonKeyRotationStage        = "StageInProgress"
	reasonKeyRotationVerifying    = "Verifying"
	reasonKeyRotationBlocked      = "Blocked"
	reasonKeyRotationSaved        = "NewKeysSaved"
	reasonKeyRotationRestarting   = "RestartingAPIServer"
	reasonKeyRotationReencrypting = "Reencrypting"
	reasonKeyRotationSettling     = "SettlingTokens"
	reasonKeyRotationLegacyTokens = "LegacyTokens"
	reasonKeyRotationComplete     = "Rotated"

	// keyRotationFinalizer holds a key rotation's TalosOperation while the nodes are between two settled
	// states: deleting it then would lose the stage, which is the only record of which keys the nodes accept.
	keyRotationFinalizer = "ichor.maesh.dev/key-rotation"

	// keyRotationNextStage is how soon the next stage is looked at: stages are separated by a status write.
	keyRotationNextStage = time.Second

	// keyRotationPoll is how often a restarting kube-apiserver is looked at.
	keyRotationPoll = 10 * time.Second

	// keyReencryptTimeout bounds one sweep over every Secret.
	keyReencryptTimeout = 10 * time.Minute

	bundleStaleReasonKeys = "KeysRotated"

	// legacyTokensShown is how many token Secrets a message names.
	legacyTokensShown = 5
)

// keyRotationUnderway reports whether op is a key rotation that has started changing nodes and has not
// finished: the control planes are not in one settled state. Used by the lock gates and the machine
// controller, which must not render or apply for a cluster in this state.
func keyRotationUnderway(op *ichorv1alpha1.TalosOperation) bool {
	return rotationUnderway(op, ichorv1alpha1.TalosOperationKindKeyRotation)
}

// activeKeyRotation returns the name of the cluster's key rotation that is under way, or "".
func activeKeyRotation(ctx context.Context, reader client.Reader, namespace, cluster string) (string, error) {
	return activeRotation(ctx, reader, namespace, cluster, ichorv1alpha1.TalosOperationKindKeyRotation)
}

// keyRotationFinalizerStep adds the finalizer to a rotation's operation and removes it once the rotation has
// settled. handled is true when the reconcile has nothing more to do.
func (r *TalosOperationReconciler) keyRotationFinalizerStep(ctx context.Context, op *ichorv1alpha1.TalosOperation) (handled bool, err error) {
	return r.rotationFinalizerStep(ctx, op, ichorv1alpha1.TalosOperationKindKeyRotation, keyRotationFinalizer, secretsbundle.KeyRotationKey)
}

// keyRun is one reconcile of a key rotation.
type keyRun struct {
	r       *TalosOperationReconciler
	op      *ichorv1alpha1.TalosOperation
	cluster *ichorv1alpha1.TalosCluster
	lock    *clusterlock.Lock
	bundle  *secrets.Bundle
	kr      *secretsbundle.KeyRotation
	scrub   scrubber
}

// advanceKeyRotation runs one step of the rotation. The caller holds the cluster lock.
func (r *TalosOperationReconciler) advanceKeyRotation(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	run := &keyRun{r: r, op: op, cluster: cluster, lock: lock}

	if op.Spec.KeyRotation == nil {
		return ctrl.Result{}, run.status(ctx, ichorv1alpha1.TalosOperationPhasePaused, metav1.ConditionFalse,
			reasonKeyRotationBlocked, "the operation has no spec.keyRotation")
	}

	if r.TalosClient == nil || r.NodesReady == nil || (run.has(ichorv1alpha1.KeyRotationTargetSecretbox) && r.Reencrypt == nil) ||
		(run.has(ichorv1alpha1.KeyRotationTargetServiceAccount) && r.LegacyTokens == nil) {
		return ctrl.Result{}, run.status(ctx, ichorv1alpha1.TalosOperationPhasePaused, metav1.ConditionFalse,
			reasonOperationNoExecutor, "no Talos client or Kubernetes check is wired up for this controller")
	}

	if err := lock.Renew(ctx, operationLockIdentity(op)); err != nil {
		return renewRefusal(ctx, err)
	}

	// The Secret is read uncached: it decides which key is current, and a stale read after this controller's
	// own write would redo a finished step.
	reader := readerOrClient(r.APIReader, r.Client)

	bundle, err := loadBundle(ctx, reader, r.ControllerNamespace, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading secrets bundle to rotate %q's keys: %w", cluster.Name, err)
	}

	run.bundle = bundle
	run.scrub.add(bundleSecretMaterial(bundle)...)

	kr, err := secretsbundle.LoadKeyRotation(ctx, reader, r.ControllerNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return ctrl.Result{}, run.blocked(ctx, "reading the new keys failed: "+err.Error())
	}

	if kr != nil {
		run.kr = kr
		run.addNewKeys(kr)
	}

	rs := op.Status.KeyRotation

	switch {
	case rs == nil || rs.Stage == "":
		return run.preflight(ctx)
	case rs.Stage == ichorv1alpha1.KeyRotationStageFinalizing:
		return run.finalize(ctx)
	case rs.Stage == ichorv1alpha1.KeyRotationStageDone:
		return run.complete(ctx)
	}

	if kr == nil {
		return run.retry(ctx, fmt.Sprintf(
			"the Secret %q has no %s although the rotation is in stage %s; the new keys cannot be found, so nothing is advanced. "+
				"Restore %s from the copy made when the rotation started",
			cluster.Spec.SecretsRef.Name, secretsbundle.KeyRotationKey, rs.Stage, secretsbundle.KeyRotationKey))
	}

	switch rs.Stage {
	case ichorv1alpha1.KeyRotationStageReencrypting:
		return run.reencrypt(ctx)
	case ichorv1alpha1.KeyRotationStageSettlingTokens:
		return run.settleTokens(ctx)
	}

	return run.pass(ctx, rs.Stage)
}

// has reports whether the operation rotates target.
func (c *keyRun) has(target string) bool {
	return slices.Contains(c.op.Spec.KeyRotation.Targets, target)
}

// status writes the phase and the KeyRotation condition in one update. Every message goes through the scrubber.
func (c *keyRun) status(ctx context.Context, phase string, cond metav1.ConditionStatus, reason, message string) error {
	message = c.scrub.clean(message)

	c.op.Status.Phase = phase
	c.op.Status.Message = ""

	switch phase {
	case ichorv1alpha1.TalosOperationPhaseInProgress:
		c.op.Status.Message = message
	case ichorv1alpha1.TalosOperationPhaseCompleted:
		if c.op.Status.FinishedAt == nil {
			now := metav1.Now()
			c.op.Status.FinishedAt = &now
		}
	}

	apimeta.SetStatusCondition(&c.op.Status.Conditions, metav1.Condition{
		Type: conditionKeyRotation, Status: cond, Reason: reason, Message: message,
	})

	return c.r.Status().Update(ctx, c.op)
}

// blocked pauses the operation without advancing anything; it is retried by the caller's requeue, until the
// rotation has been blocked for rotationGiveUpAfter in a row (caRun.blocked).
func (c *keyRun) blocked(ctx context.Context, message string) error {
	if blockedFor(apimeta.FindStatusCondition(c.op.Status.Conditions, conditionKeyRotation), reasonKeyRotationBlocked) >=
		rotationGiveUpAfter {
		return c.r.giveUpBlocked(ctx, c.op, conditionKeyRotation, c.scrub.clean(message))
	}

	return c.status(ctx, ichorv1alpha1.TalosOperationPhasePaused, metav1.ConditionFalse, reasonKeyRotationBlocked, message)
}

func (c *keyRun) retry(ctx context.Context, message string) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: operationRequeueInterval}, c.blocked(ctx, message)
}

// controlPlanes returns the approved control planes in rollout order. Only they carry the two documents a
// key rotation edits: a worker is never applied to.
func (c *keyRun) controlPlanes(ctx context.Context) ([]*ichorv1alpha1.TalosMachine, error) {
	list, err := deleteguard.ApprovedMachines(ctx, readerOrClient(c.r.APIReader, c.r.Client),
		c.cluster.Namespace, c.cluster.Name, deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, err
	}

	out := ptrs(list)
	upgrade.SortByOrder(out)

	return out, nil
}

// withNode dials m with the cluster's bundle (a key rotation never changes the Talos API CA) and runs fn
// with ctx bounded by limit, the dial itself by etcdDialTimeout.
func (c *keyRun) withNode(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, limit time.Duration, fn func(context.Context, TalosClient) error,
) error {
	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	talos, err := c.r.TalosClient(dialCtx, c.cluster, m, c.bundle)

	cancel()

	if err != nil {
		return fmt.Errorf("dialing %q: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	callCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	return fn(callCtx, talos)
}

// render renders m for rotation's stage ("" renders what the bundle says, no rotation).
func (c *keyRun) render(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, patches []ichorv1alpha1.TalosConfigPatch,
	rotation *render.KeyRotation,
) ([]byte, string, error) {
	sys, err := render.ResolveSystemInputs(ctx, c.r.Client, c.r.ControllerNamespace, c.cluster)
	if err != nil {
		return nil, "", err
	}

	sys.KeyRotation = rotation

	return render.Render(c.cluster, m, bundle, patches, sys)
}

func (c *keyRun) patches(ctx context.Context) ([]ichorv1alpha1.TalosConfigPatch, error) {
	var patches ichorv1alpha1.TalosConfigPatchList
	if err := c.r.List(ctx, &patches, client.InNamespace(c.cluster.Namespace)); err != nil {
		return nil, err
	}

	return patches.Items, nil
}

// nodesReady fails unless every Kubernetes Node is Ready, asked through first's admin kubeconfig.
func (c *keyRun) nodesReady(ctx context.Context, first *ichorv1alpha1.TalosMachine) error {
	return c.withNode(ctx, first, etcdDialTimeout, c.r.checkNodesReady)
}

// keyIdentifiers returns the public identifiers of the keys the rotation moves, from the old bundle and the new keys.
func (c *keyRun) keyIdentifiers(kr *secretsbundle.KeyRotation) ([]ichorv1alpha1.KeyRotationKey, error) {
	var out []ichorv1alpha1.KeyRotationKey

	if kr.ServiceAccount != nil {
		prev, err := secretsbundle.ServiceAccountFingerprint(c.bundle.Certs.K8sServiceAccount)
		if err != nil {
			return nil, fmt.Errorf("service-account key: %w", err)
		}

		next, err := secretsbundle.ServiceAccountFingerprint(kr.ServiceAccount)
		if err != nil {
			return nil, fmt.Errorf("service-account key: %w", err)
		}

		out = append(out, ichorv1alpha1.KeyRotationKey{Name: ichorv1alpha1.KeyRotationTargetServiceAccount, Previous: prev, Next: next})
	}

	if kr.Secretbox != "" {
		out = append(out, ichorv1alpha1.KeyRotationKey{
			Name:     ichorv1alpha1.KeyRotationTargetSecretbox,
			Previous: secretsbundle.SecretboxID(c.bundle.Secrets.SecretboxEncryptionSecret), Next: secretsbundle.SecretboxID(kr.Secretbox),
		})
	}

	return out, nil
}

// setStageKeys writes, from rs.Stage, which key is active and which are accepted in it.
func setStageKeys(rs *ichorv1alpha1.TalosOperationKeyRotationStatus) {
	for i := range rs.Keys {
		key := &rs.Keys[i]

		switch rs.Stage {
		case ichorv1alpha1.KeyRotationStageAccepting:
			key.Active, key.Accepted = key.Previous, []string{key.Next}
		case ichorv1alpha1.KeyRotationStageIssuing, ichorv1alpha1.KeyRotationStageReencrypting, ichorv1alpha1.KeyRotationStageSettlingTokens:
			key.Active, key.Accepted = key.Next, []string{key.Previous}
		default:
			key.Active, key.Accepted = key.Next, nil
		}
	}
}

// nextKeyStage is the stage that follows a verified one, skipping those the targets do not need.
func (c *keyRun) nextKeyStage(stage string) string {
	switch stage {
	case ichorv1alpha1.KeyRotationStageAccepting:
		return ichorv1alpha1.KeyRotationStageIssuing
	case ichorv1alpha1.KeyRotationStageIssuing:
		if c.has(ichorv1alpha1.KeyRotationTargetSecretbox) {
			return ichorv1alpha1.KeyRotationStageReencrypting
		}

		return c.afterReencrypting()
	case ichorv1alpha1.KeyRotationStageReencrypting:
		return c.afterReencrypting()
	case ichorv1alpha1.KeyRotationStageSettlingTokens:
		return ichorv1alpha1.KeyRotationStageDropping
	default:
		return ichorv1alpha1.KeyRotationStageFinalizing
	}
}

func (c *keyRun) afterReencrypting() string {
	if c.has(ichorv1alpha1.KeyRotationTargetServiceAccount) {
		return ichorv1alpha1.KeyRotationStageSettlingTokens
	}

	return ichorv1alpha1.KeyRotationStageDropping
}

// advance moves the operation to the stage after the current one. status.machines shows the stage under way,
// so the next one starts empty.
func (c *keyRun) advance(ctx context.Context, from, message string) (ctrl.Result, error) {
	rs := c.op.Status.KeyRotation
	rs.Stage = c.nextKeyStage(from)
	rs.Restart = nil
	setStageKeys(rs)
	c.op.Status.Machines = nil

	return ctrl.Result{RequeueAfter: keyRotationNextStage},
		c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue, reasonKeyRotationVerifying, message)
}

// preflight refuses to start unless every control plane answers, runs what Ichor would render for it, its
// kube-apiserver is ready and every Node is Ready: a rotation begun on a broken cluster cannot be told from
// one that broke it. Then it saves the new keys and enters the first stage. Nothing is written before it passes.
func (c *keyRun) preflight(ctx context.Context) (ctrl.Result, error) {
	cps, err := c.controlPlanes(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(cps) == 0 {
		return c.retry(ctx, "the cluster has no approved control plane to rotate the keys of")
	}

	contract, err := coreconfig.ParseContractFromVersion(c.cluster.Spec.VersionContract)
	if err != nil {
		return c.retry(ctx, "invalid versionContract")
	}

	if !contract.MultidocKubernetesConfigSupported() {
		return c.retry(ctx, fmt.Sprintf("spec.versionContract %q keeps the service-account key and the secretbox secret in single "+
			"fields that cannot hold two keys at once; raise the contract to v1.14 or later before rotating them", c.cluster.Spec.VersionContract))
	}

	if c.has(ichorv1alpha1.KeyRotationTargetSecretbox) && c.bundle.Secrets.SecretboxEncryptionSecret == "" {
		return c.retry(ctx, "the secrets bundle has no secretbox secret (the cluster encrypts with aescbc), so there is no secretbox key to rotate")
	}

	// A keyrotation.yaml left by an earlier attempt that never reached a machine is adopted, not replaced, as
	// long as it holds the keys this operation names.
	kr := c.kr
	if kr == nil {
		if kr, err = secretsbundle.NewKeyRotation(c.bundle, c.op.Spec.KeyRotation.Targets); err != nil {
			return ctrl.Result{}, err
		}

		c.addNewKeys(kr)
	} else if (kr.ServiceAccount != nil) != c.has(ichorv1alpha1.KeyRotationTargetServiceAccount) ||
		(kr.Secretbox != "") != c.has(ichorv1alpha1.KeyRotationTargetSecretbox) {
		return c.retry(ctx, fmt.Sprintf("the Secret already holds %s from another rotation and it does not hold the keys this operation "+
			"names; remove it or start the rotation it belongs to", secretsbundle.KeyRotationKey))
	}

	patches, err := c.patches(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, m := range cps {
		if res, blocked, err := c.preflightMachine(ctx, m, patches, kr); blocked {
			return res, err
		}
	}

	if err := c.nodesReady(ctx, cps[0]); err != nil {
		return c.retry(ctx, "not starting the rotation: "+err.Error())
	}

	// Save the new keys before any machine is asked to use them.
	if c.kr == nil {
		if err := c.saveKeyRotation(ctx, kr); err != nil {
			return ctrl.Result{}, fmt.Errorf("saving the new keys before rotating: %w", err)
		}

		c.kr = kr
	}

	keys, err := c.keyIdentifiers(kr)
	if err != nil {
		return ctrl.Result{}, err
	}

	c.op.Status.KeyRotation = &ichorv1alpha1.TalosOperationKeyRotationStatus{Stage: ichorv1alpha1.KeyRotationStageAccepting, Keys: keys}
	setStageKeys(c.op.Status.KeyRotation)

	return ctrl.Result{RequeueAfter: keyRotationNextStage}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
		reasonKeyRotationSaved, "the new keys are saved; adding them next to the old ones on every control plane")
}

func (c *keyRun) addNewKeys(kr *secretsbundle.KeyRotation) {
	if kr.ServiceAccount != nil {
		c.scrub.add(string(kr.ServiceAccount.Key))
	}

	c.scrub.add(kr.Secretbox)
}

// preflightMachine checks one control plane. blocked is true when the operation was paused or errored.
func (c *keyRun) preflightMachine(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, patches []ichorv1alpha1.TalosConfigPatch, kr *secretsbundle.KeyRotation,
) (ctrl.Result, bool, error) {
	_, plain, err := c.render(ctx, m, c.bundle, patches, nil)
	if err != nil {
		res, err := c.retry(ctx, fmt.Sprintf("rendering %q's configuration failed: %v", m.Name, err))

		return res, true, err
	}

	_, first, err := c.render(ctx, m, c.bundle, patches, &render.KeyRotation{Stage: ichorv1alpha1.KeyRotationStageAccepting, New: kr})
	if err != nil {
		res, err := c.retry(ctx, fmt.Sprintf("rendering %q's first stage failed: %v", m.Name, err))

		return res, true, err
	}

	if first == plain {
		res, err := c.retry(ctx, fmt.Sprintf("a TalosConfigPatch overrides the Kubernetes service-account or encryption document of %q, so "+
			"adding the new keys would change nothing; remove the override before rotating", m.Name))

		return res, true, err
	}

	err = c.withNode(ctx, m, etcdDialTimeout, func(ctx context.Context, talos TalosClient) error {
		if _, err := talos.Version(ctx); err != nil {
			return fmt.Errorf("%q does not answer: %w", m.Name, err)
		}

		current, err := talos.CurrentConfig(ctx)
		if err != nil {
			return fmt.Errorf("reading %q's configuration: %w", m.Name, err)
		}

		if render.Hash(current) != plain {
			return fmt.Errorf("%q does not run the configuration Ichor renders for it; converge it before rotating", m.Name)
		}

		if err := talos.APIServerReady(ctx); err != nil {
			return fmt.Errorf("the kube-apiserver of %q is not ready: %w", m.Name, err)
		}

		return nil
	})
	if err != nil {
		res, err := c.retry(ctx, "not starting the rotation: "+err.Error())

		return res, true, err
	}

	return ctrl.Result{}, false, nil
}

// saveKeyRotation writes keyrotation.yaml into the bundle Secret, once and only if absent.
func (c *keyRun) saveKeyRotation(ctx context.Context, kr *secretsbundle.KeyRotation) error {
	raw, err := kr.Marshal()
	if err != nil {
		return errors.New("encoding the new keys")
	}

	var secret corev1.Secret
	if err := readerOrClient(c.r.APIReader, c.r.Client).Get(ctx, c.r.bundleKey(c.cluster), &secret); err != nil {
		return err
	}

	if _, exists := secret.Data[secretsbundle.KeyRotationKey]; exists {
		return errors.New("keyrotation.yaml appeared while the new keys were being generated")
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}

	secret.Data[secretsbundle.KeyRotationKey] = raw

	return c.r.updateBundle(ctx, c.cluster, &secret)
}

// pass makes every control plane run the configuration of stage one at a time, waiting for each kube-apiserver
// to restart and answer, then verifies the stage.
func (c *keyRun) pass(ctx context.Context, stage string) (ctrl.Result, error) {
	cps, err := c.controlPlanes(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(cps) == 0 {
		return c.retry(ctx, "the cluster has no approved control plane to rotate the keys of")
	}

	patches, err := c.patches(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	rotation := &render.KeyRotation{Stage: stage, New: c.kr}
	rs := c.op.Status.KeyRotation

	// A restart under way is waited for before any other control plane is touched.
	if rs.Restart != nil {
		if res, wait, err := c.awaitRestart(ctx, stage, cps, patches, rotation); wait {
			return res, err
		}
	}

	for _, m := range cps {
		rendered, hash, err := c.render(ctx, m, c.bundle, patches, rotation)
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("rendering %q's %s configuration failed: %v", m.Name, stage, err))
		}

		applied := false

		err = c.withNode(ctx, m, upgradeCallTimeout, func(ctx context.Context, talos TalosClient) error {
			current, err := talos.CurrentConfig(ctx)
			if err != nil {
				return fmt.Errorf("reading %q's configuration: %w", m.Name, err)
			}

			if render.Hash(current) == hash {
				return nil
			}

			if _, err := talos.DryRunApply(ctx, rendered); err != nil {
				return fmt.Errorf("dry-run apply to %q failed: %w", m.Name, err)
			}

			baseline, err := talos.APIServerInstance(ctx)
			if err != nil {
				return fmt.Errorf("reading %q's kube-apiserver before restarting it: %w", m.Name, err)
			}

			// Recorded before the apply, so a controller that dies in between still knows which container to wait to
			// see replaced; a record whose apply never happened is dropped again by awaitRestart.
			rs.Restart = &ichorv1alpha1.KeyRotationRestart{Machine: m.Name, Instance: baseline}

			if err := c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue, reasonKeyRotationRestarting,
				fmt.Sprintf("stage %s: applying to %q", stage, m.Name)); err != nil {
				return err
			}

			if err := talos.ApplyNoReboot(ctx, rendered); err != nil {
				return fmt.Errorf("applying the %s configuration to %q failed: %w", stage, m.Name, err)
			}

			applied = true

			return nil
		})
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("stage %s did not advance: %v%s", stage, err, refusalNote(err)))
		}

		if applied {
			c.r.markMachine(c.op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)

			return ctrl.Result{RequeueAfter: keyRotationPoll}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
				reasonKeyRotationRestarting, fmt.Sprintf("stage %s: applied to %q; waiting for its kube-apiserver to restart and answer", stage, m.Name))
		}
	}

	// Every control plane runs the stage's configuration and its kube-apiserver has come back. Verify the cluster
	// is healthy before moving on.
	for _, m := range cps {
		err := c.withNode(ctx, m, etcdDialTimeout, func(ctx context.Context, talos TalosClient) error {
			if _, err := talos.Version(ctx); err != nil {
				return fmt.Errorf("ichor cannot reach %q: %w", m.Name, err)
			}

			if err := talos.APIServerReady(ctx); err != nil {
				return fmt.Errorf("the kube-apiserver of %q is not ready: %w", m.Name, err)
			}

			return nil
		})
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("stage %s is not verified, so the next stage is not started: %v%s", stage, err, refusalNote(err)))
		}
	}

	if err := c.nodesReady(ctx, cps[0]); err != nil {
		return c.retry(ctx, fmt.Sprintf("stage %s is not verified, so the next stage is not started: %v%s", stage, err, refusalNote(err)))
	}

	// Every control plane signs with the new key: pods started from now on get tokens of it. Ichor's own, which
	// hold tokens of the old key until the kubelet refreshes them, are rolled now, so that dropping the old key
	// cannot lock the controller that runs this rotation out of the API.
	if stage == ichorv1alpha1.KeyRotationStageIssuing && c.has(ichorv1alpha1.KeyRotationTargetServiceAccount) {
		if err := c.restartIchorWorkloads(ctx); err != nil {
			return c.retry(ctx, "restarting Ichor's own controller and ichor-ui, so that they hold tokens of the new key, failed: "+err.Error())
		}
	}

	return c.advance(ctx, stage, fmt.Sprintf("stage %s verified on %d control plane(s)", stage, len(cps)))
}

// awaitRestart waits for the kube-apiserver recorded in status.keyRotation.restart to run in a container other
// than the recorded one and to answer /readyz, with the machine on the stage's configuration. wait is false
// when the caller may go on, with the record cleared in memory (the next status write persists it).
func (c *keyRun) awaitRestart(
	ctx context.Context, stage string, cps []*ichorv1alpha1.TalosMachine, patches []ichorv1alpha1.TalosConfigPatch, rotation *render.KeyRotation,
) (ctrl.Result, bool, error) {
	rs := c.op.Status.KeyRotation
	restart := rs.Restart

	i := slices.IndexFunc(cps, func(m *ichorv1alpha1.TalosMachine) bool { return m.Name == restart.Machine })
	if i < 0 {
		rs.Restart = nil // the machine is gone; whatever is left is visited by the loop

		return ctrl.Result{}, false, nil
	}

	m := cps[i]

	_, hash, err := c.render(ctx, m, c.bundle, patches, rotation)
	if err != nil {
		res, err := c.retry(ctx, fmt.Sprintf("rendering %q's %s configuration failed: %v", m.Name, stage, err))

		return res, true, err
	}

	var why string

	err = c.withNode(ctx, m, etcdDialTimeout, func(ctx context.Context, talos TalosClient) error {
		current, err := talos.CurrentConfig(ctx)
		if err != nil {
			return fmt.Errorf("reading %q's configuration: %w", m.Name, err)
		}

		if render.Hash(current) != hash {
			// The controller stopped between the record and the apply: apply again.
			rs.Restart = nil

			return nil
		}

		instance, err := talos.APIServerInstance(ctx)
		if err != nil {
			return fmt.Errorf("reading the kube-apiserver of %q: %w", m.Name, err)
		}

		switch instance {
		case "":
			// Not "unchanged": Talos lists no kube-apiserver container at all (it is being replaced, or its status is not
			// reported yet). Saying "still runs in the container it ran before" here misled an operator on hardware.
			why = fmt.Sprintf("Talos reports no kube-apiserver container on %q right now", m.Name)

			return nil
		case restart.Instance:
			why = fmt.Sprintf("the kube-apiserver of %q still runs in the container it ran before the apply", m.Name)

			return nil
		}

		if err := talos.APIServerReady(ctx); err != nil {
			why = fmt.Sprintf("the kube-apiserver of %q restarted but is not ready: %v", m.Name, err)

			return nil
		}

		rs.Restart = nil

		return nil
	})
	if err != nil {
		res, err := c.retry(ctx, fmt.Sprintf("stage %s did not advance: %v%s", stage, err, refusalNote(err)))

		return res, true, err
	}

	if rs.Restart != nil {
		return ctrl.Result{RequeueAfter: keyRotationPoll}, true, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
			reasonKeyRotationRestarting, fmt.Sprintf("stage %s: waiting for the new configuration to take effect: %s (last checked %s; "+
				"an older time means Ichor's controller is not running, see its pod)", stage, why, time.Now().UTC().Format("15:04Z")))
	}

	c.r.markMachine(c.op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseCompleted)

	return ctrl.Result{}, false, nil
}

// reencrypt runs one sweep over every Secret; the stage ends after a sweep that stored nothing again.
func (c *keyRun) reencrypt(ctx context.Context) (ctrl.Result, error) {
	cps, err := c.controlPlanes(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(cps) == 0 {
		return c.retry(ctx, "the cluster has no approved control plane to reach the Kubernetes API through")
	}

	var res ReencryptResult

	err = c.withNode(ctx, cps[0], keyReencryptTimeout, func(ctx context.Context, talos TalosClient) error {
		var err error

		res, err = c.r.Reencrypt(ctx, talos)

		return err
	})
	if err != nil {
		return c.retry(ctx, "re-encrypting the Secrets did not finish, so the old secretbox key stays: "+err.Error()+refusalNote(err))
	}

	rs := c.op.Status.KeyRotation

	sweeps := int32(1)
	if rs.Reencryption != nil {
		sweeps += rs.Reencryption.Sweeps
	}

	rs.Reencryption = &ichorv1alpha1.KeyRotationReencryption{Sweeps: sweeps, Rewritten: res.Rewritten, Scanned: res.Scanned}

	if res.Clean() {
		return c.advance(ctx, ichorv1alpha1.KeyRotationStageReencrypting, fmt.Sprintf(
			"every Secret is stored under the new key: sweep %d looked at %d Secret(s) and rewrote none", sweeps, res.Scanned))
	}

	return ctrl.Result{RequeueAfter: keyRotationNextStage}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
		reasonKeyRotationReencrypting, fmt.Sprintf("sweep %d rewrote %d of %d Secret(s) and could not look at %d; another sweep follows",
			sweeps, res.Rewritten, res.Scanned, res.Conflicts))
}

// settleTokens keeps the old service-account key while the projected tokens it signed are re-issued, then until
// no token Secret depends on it (or the operator accepted losing them).
func (c *keyRun) settleTokens(ctx context.Context) (ctrl.Result, error) {
	rs := c.op.Status.KeyRotation

	settle := ichorv1alpha1.DefaultKeyRotationTokenSettle
	if t := c.op.Spec.KeyRotation.TokenSettle; t != nil {
		settle = t.Duration
	}

	// The clock is in status, so it survives a controller restart and is not reset by a pause.
	if rs.SettlingSince == nil {
		now := metav1.Now()
		rs.SettlingSince = &now

		return ctrl.Result{RequeueAfter: min(settle, operationRequeueInterval)}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress,
			metav1.ConditionTrue, reasonKeyRotationSettling, fmt.Sprintf("the new service-account key signs; keeping the old one for %s "+
				"so the tokens it signed are re-issued. Tokens kept outside the cluster, or asked for with a longer lifetime, stop working "+
				"when it is dropped", settle))
	}

	if left := settle - time.Since(rs.SettlingSince.Time); left > 0 {
		return ctrl.Result{RequeueAfter: min(left, operationRequeueInterval)}, nil
	}

	cps, err := c.controlPlanes(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(cps) == 0 {
		return c.retry(ctx, "the cluster has no approved control plane to reach the Kubernetes API through")
	}

	if err := c.nodesReady(ctx, cps[0]); err != nil {
		return c.retry(ctx, "the old service-account key stays until the cluster is healthy: "+err.Error()+refusalNote(err))
	}

	if !c.op.Spec.KeyRotation.IgnoreLegacyTokens {
		old, err := c.oldServiceAccountPublicKey()
		if err != nil {
			return ctrl.Result{}, err
		}

		var found []string

		err = c.withNode(ctx, cps[0], keyReencryptTimeout, func(ctx context.Context, talos TalosClient) error {
			var err error

			found, err = c.r.LegacyTokens(ctx, talos, old)

			return err
		})
		if err != nil {
			return c.retry(ctx, "looking for token Secrets signed by the old key failed, so it stays: "+err.Error()+refusalNote(err))
		}

		if len(found) > 0 {
			slices.Sort(found)

			shown := strings.Join(found[:min(len(found), legacyTokensShown)], ", ")

			return ctrl.Result{RequeueAfter: operationRequeueInterval}, c.status(ctx, ichorv1alpha1.TalosOperationPhasePaused,
				metav1.ConditionFalse, reasonKeyRotationLegacyTokens, fmt.Sprintf("%d Secret(s) of type kubernetes.io/service-account-token hold a "+
					"token signed by the old key and stop working when it is dropped, among them %s. Delete the `token` key of each (the "+
					"controller manager writes a new one) or delete the Secret; the rotation continues by itself. To drop the old key anyway, "+
					"recreate the operation with spec.keyRotation.ignoreLegacyTokens", len(found), shown))
		}
	}

	// Ichor's own pods hold tokens of the new key only once their Deployments have rolled out (restartIchorWorkloads).
	var waiting string

	if ns := c.r.ControllerNamespace; ns != "" {
		if waiting, err = ichorRolloutWaiting(ctx, readerOrClient(c.r.APIReader, c.r.Client), ns); err != nil {
			return c.retry(ctx, "reading Ichor's own Deployments failed, so the old service-account key stays: "+err.Error())
		}
	}

	if waiting != "" {
		return ctrl.Result{RequeueAfter: keyRotationPoll}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
			reasonKeyRotationSettling, "the old service-account key stays until Ichor's own pods hold tokens of the new one: "+waiting)
	}

	return c.advance(ctx, ichorv1alpha1.KeyRotationStageSettlingTokens, "the old service-account key is no longer needed; removing it")
}

// oldServiceAccountPublicKey is the public half of the key the rotation retires, from the bundle.
func (c *keyRun) oldServiceAccountPublicKey() ([]byte, error) {
	key, err := c.bundle.Certs.K8sServiceAccount.GetKey()
	if err != nil {
		return nil, errors.New("the bundle's service-account key is not a PEM key")
	}

	return key.GetPublicKeyPEM(), nil
}

// finalize replaces secrets.yaml with the new keys, records what was retired and removes keyrotation.yaml, in
// one Secret update that fails if anyone changed the Secret meanwhile; raises BundleBackupStale; and checks
// every control plane against the replaced bundle.
func (c *keyRun) finalize(ctx context.Context) (ctrl.Result, error) {
	bundle := c.bundle

	if c.kr != nil {
		replaced, err := c.replaceBundle(ctx)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("replacing the secrets bundle: %w", err)
		}

		bundle = replaced
	} else if !c.holdsNewKeys(bundle) {
		return c.retry(ctx, "the secrets bundle neither carries the new keys nor has a keyrotation.yaml to take them from")
	}

	if err := c.markBundleBackupStale(ctx); err != nil {
		return ctrl.Result{}, err
	}

	cps, err := c.controlPlanes(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	patches, err := c.patches(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	c.bundle = bundle

	for _, m := range cps {
		_, hash, err := c.render(ctx, m, bundle, patches, nil)
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("rendering %q's configuration failed: %v", m.Name, err))
		}

		err = c.withNode(ctx, m, etcdDialTimeout, func(ctx context.Context, talos TalosClient) error {
			current, err := talos.CurrentConfig(ctx)
			if err != nil {
				return fmt.Errorf("ichor cannot reach %q: %w", m.Name, err)
			}

			if render.Hash(current) != hash {
				return fmt.Errorf("%q does not run the configuration the replaced bundle renders", m.Name)
			}

			return talos.APIServerReady(ctx)
		})
		if err != nil {
			return c.retry(ctx, "the replaced bundle is not verified: "+err.Error())
		}
	}

	if len(cps) > 0 {
		if err := c.nodesReady(ctx, cps[0]); err != nil {
			return c.retry(ctx, "the replaced bundle is not verified: "+err.Error())
		}
	}

	// Done is the last thing this reconcile verified, so the operation completes now.
	c.op.Status.KeyRotation.Stage = ichorv1alpha1.KeyRotationStageDone
	setStageKeys(c.op.Status.KeyRotation)

	return c.complete(ctx)
}

// holdsNewKeys reports whether bundle already carries the keys this rotation is moving to.
func (c *keyRun) holdsNewKeys(bundle *secrets.Bundle) bool {
	for _, key := range c.op.Status.KeyRotation.Keys {
		switch key.Name {
		case ichorv1alpha1.KeyRotationTargetServiceAccount:
			if fp, err := secretsbundle.ServiceAccountFingerprint(bundle.Certs.K8sServiceAccount); err != nil || fp != key.Next {
				return false
			}
		case ichorv1alpha1.KeyRotationTargetSecretbox:
			if secretsbundle.SecretboxID(bundle.Secrets.SecretboxEncryptionSecret) != key.Next {
				return false
			}
		}
	}

	return true
}

// replaceBundle writes the new keys into secrets.yaml, records the retired ones and removes keyrotation.yaml,
// in one update that fails if anyone changed the Secret meanwhile.
func (c *keyRun) replaceBundle(ctx context.Context) (*secrets.Bundle, error) {
	replaced := c.kr.Apply(c.bundle)

	raw, err := yaml.Marshal(replaced)
	if err != nil {
		return nil, errors.New("encoding the replaced bundle")
	}

	var secret corev1.Secret
	if err := readerOrClient(c.r.APIReader, c.r.Client).Get(ctx, c.r.bundleKey(c.cluster), &secret); err != nil {
		return nil, err
	}

	retired := &secretsbundle.Retired{}

	if existing, ok := secret.Data[secretsbundle.RetiredKeysKey]; ok {
		if retired, err = secretsbundle.ParseRetired(existing); err != nil {
			return nil, err
		}
	}

	entry := secretsbundle.RetiredKey{RetiredAt: time.Now().UTC()}

	if c.kr.Secretbox != "" {
		entry.Secretbox = c.bundle.Secrets.SecretboxEncryptionSecret
	}

	if c.kr.ServiceAccount != nil {
		if entry.ServiceAccount, err = secretsbundle.ServiceAccountFingerprint(c.bundle.Certs.K8sServiceAccount); err != nil {
			return nil, err
		}
	}

	retired.Add(entry)

	rawRetired, err := retired.Marshal()
	if err != nil {
		return nil, errors.New("encoding the retired keys")
	}

	secret.Data[secretsbundle.SecretKey] = raw
	secret.Data[secretsbundle.RetiredKeysKey] = rawRetired
	delete(secret.Data, secretsbundle.KeyRotationKey)

	if err := c.r.updateBundle(ctx, c.cluster, &secret); err != nil {
		return nil, err
	}

	return replaced, nil
}

// markBundleBackupStale raises BundleBackupStale. The message carries no secret; it names the command that clears it.
func (c *keyRun) markBundleBackupStale(ctx context.Context) error {
	return c.r.raiseBundleBackupStale(ctx, c.cluster, bundleStaleReasonKeys, fmt.Sprintf(
		"A key rotation replaced the secrets bundle. The copy saved on your machine no longer matches the "+
			"cluster, and recovering from a total loss needs the new one together with the retired keys. Run `ichor export bundle %s` "+
			"and keep both files safe.", c.cluster.Name))
}

// complete ends the operation: the lock is released, the phase is Completed.
func (c *keyRun) complete(ctx context.Context) (ctrl.Result, error) {
	if err := c.lock.Release(ctx, operationLockIdentity(c.op)); err != nil {
		return ctrl.Result{}, err
	}

	message := "the keys are replaced; export the new secrets bundle (it now carries the retired keys too)"
	if c.has(ichorv1alpha1.KeyRotationTargetSecretbox) {
		message += ", and defragment etcd on each control plane so that old revisions encrypted with the old key are dropped"
	}

	return ctrl.Result{}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseCompleted, metav1.ConditionFalse, reasonKeyRotationComplete, message)
}
