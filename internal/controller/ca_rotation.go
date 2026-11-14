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

// This file implements the TalosOperationKindCARotation state machine: the replacement
// of the Talos API CA and the Kubernetes API CA, in the three passes `talosctl rotate-ca` proves
// (accept the new CA, make it the issuer, drop the old one), each a full pass over every machine and
// each verified before the next. TalosOperationReconciler dispatches here after taking the cluster
// lock.
//
// The stage lives in op.status.caRotation and nowhere else; which machines a stage still has to
// visit is re-derived from what each machine reports, so a controller killed at any point resumes
// from status with no recovery path.
//
// The new CAs' keys are written to the bundle Secret's rotation.yaml before any machine is told
// to trust them, so no crash can leave nodes trusting a CA whose key nobody holds. secrets.yaml
// is replaced, and rotation.yaml removed, in one Secret update after the last pass is verified.
//
//	"" ........ preflight (every machine reachable and converged, Kubernetes Ready), save the new
//	            CAs to rotation.yaml, then -> Accepting
//	Accepting . new CA added to every machine's accepted CAs (old issues)      -> AwaitingCredentials
//	AwaitingCredentials  nothing changes until the operator has credentials that work with both CAs:
//	            `ichor create credentials` writes them and sets the hand-off annotation   -> Issuing
//	Issuing ... new CA issues, old is accepted                                 -> Dropping
//	Dropping .. old CA removed from the accepted CAs                           -> Finalizing
//	Finalizing  Secret replaced, BundleBackupStale raised, Ichor's dial and the machines'
//	            configuration checked against the replaced bundle, the aggregator ConfigMap
//	            recreated with the new CA alone, CoreDNS, kube-proxy, the
//	            CNI and ichor-ui restarted once to load the new CA             -> Done
//
// With the etcd CA among the targets each pass is followed by a rolling reboot of the control planes
// (ca_rotation_targets.go): Talos does not restart etcd on a configuration change. Deleting the operation
// before the point of no return abandons the rotation (rotation_abandon.go).
//
// A step that cannot be verified (a machine does not answer Ichor, a Node is not Ready, an apply
// is refused) never advances the stage: the operation is Paused with the reason and tried again
// every operationRequeueInterval, so it resumes by itself once the cause is gone.
package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/kubetrust"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

const (
	// conditionCARotation is the rotation's own progress line: True while it advances, False while
	// it is blocked, with the reason.
	conditionCARotation = "CARotation"

	reasonOperationCARotating = "ClusterCARotating"

	reasonCARotationStage     = "StageInProgress"
	reasonCARotationVerifying = "Verifying"
	reasonCARotationBlocked   = "Blocked"

	reasonCARotationAwaitingNodes = "AwaitingNodes"
	reasonCARotationSettlingNodes = "SettlingNodes"
	reasonCARotationSaved         = "NewCAsSaved"
	reasonCARotationSettling      = "SettlingClients"

	// caClientSettleDelay is how long pods get, once kube-root-ca.crt carries the new Kubernetes CA,
	// to see it in their projected ca.crt: kubelet refreshes projected volumes about every minute.
	caClientSettleDelay      = 2 * time.Minute
	reasonCARotationComplete = "Rotated"
	// reasonCARotationClientsNotBack ends a rotation whose restarted clients were not available in time.
	reasonCARotationClientsNotBack = "RotatedClientsNotBack"

	// reasonCARotationAwaitingCredentials: the operation waits for the operator's credential hand-off.
	reasonCARotationAwaitingCredentials = "AwaitingCredentials"

	// caRotationFinalizer holds a rotation's TalosOperation while the nodes are between two
	// settled states: deleting it then would lose the stage, which is the only record of which CA
	// the nodes accept and which issues. It is removed when the rotation is Done.
	caRotationFinalizer = "ichor.maesh.dev/ca-rotation"

	// caRotationNextStage is how soon the next stage is looked at: stages are separated by a status
	// write, not by waiting.
	caRotationNextStage = time.Second

	talosCAName       = ichorv1alpha1.CARotationTargetTalos
	kubernetesCAName  = ichorv1alpha1.CARotationTargetKubernetes
	etcdCAName        = ichorv1alpha1.CARotationTargetEtcd
	aggregatorCAName  = ichorv1alpha1.CARotationTargetAggregator
	bundleStaleReason = "CARotated"
)

// caKubeletSettleDelay is how long the Nodes must stay Ready after the Dropping stage before the first
// client is restarted: longer than the node-monitor grace period (40 s) plus a kubelet restart.
// A variable so tests need not wait.
var caKubeletSettleDelay = 90 * time.Second

// rotationGiveUpAfter is how long a CA or key rotation may stay blocked on one step, pass after pass,
// before it gives up: the operation pauses with a FailureReason and a Warning Event, which lets the cluster
// lock go to other work. Thirty minutes outlasts a node reboot and the waits after one, and a timed-out
// apply (10 minutes) on its own, so one timeout does not end a rotation that would have finished on the next
// pass. A variable so tests need not wait.
var rotationGiveUpAfter = 30 * time.Minute

// NodesReadyFunc reports the names of the Kubernetes Nodes that are not Ready, through the
// Kubernetes API reached with talos's admin kubeconfig. A seam so tests never reach a cluster;
// cmd/main.go wires NewNodesReady.
type NodesReadyFunc func(ctx context.Context, talos TalosClient) (notReady []string, err error)

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosoperations/finalizers,verbs=update
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// The bundle Secret's name is spec.secretsRef.name, so RBAC cannot narrow this to one object;
// updateBundle is the code-level guard.
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// caRotationUnderway reports whether op is a CA rotation that has started changing nodes and has
// not finished: the cluster's nodes are not in one settled state. Used by the lock gates and by the
// machine controller, which must not render or apply for a cluster in this state.
func caRotationUnderway(op *ichorv1alpha1.TalosOperation) bool {
	return rotationUnderway(op, ichorv1alpha1.TalosOperationKindCARotation)
}

// activeCARotation returns the name of the cluster's CA rotation that is under way, or "".
func activeCARotation(ctx context.Context, reader client.Reader, namespace, cluster string) (string, error) {
	return activeRotation(ctx, reader, namespace, cluster, ichorv1alpha1.TalosOperationKindCARotation)
}

// caRotationFinalizerStep adds the finalizer to a rotation's operation and removes it once the
// rotation has settled. handled is true when the reconcile has nothing more to do.
func (r *TalosOperationReconciler) caRotationFinalizerStep(ctx context.Context, op *ichorv1alpha1.TalosOperation) (handled bool, err error) {
	return r.rotationFinalizerStep(ctx, op, ichorv1alpha1.TalosOperationKindCARotation, caRotationFinalizer, secretsbundle.RotationKey)
}

// caRun is one reconcile of a rotation: what was read, and what must never be written anywhere.
type caRun struct {
	r       *TalosOperationReconciler
	op      *ichorv1alpha1.TalosOperation
	cluster *ichorv1alpha1.TalosCluster
	lock    *clusterlock.Lock
	bundle  *secrets.Bundle
	rot     *secretsbundle.Rotation
	scrub   scrubber
	// restartNote names what did not come back after the clients' restart; it ends the completion message.
	restartNote string
}

// advanceCARotation runs one step of the rotation. The caller holds the cluster lock.
func (r *TalosOperationReconciler) advanceCARotation(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	run := &caRun{r: r, op: op, cluster: cluster, lock: lock}

	if r.TalosClient == nil || r.NodesReady == nil {
		return ctrl.Result{}, run.status(ctx, ichorv1alpha1.TalosOperationPhasePaused, metav1.ConditionFalse,
			reasonOperationNoExecutor, "no Talos client or Kubernetes readiness check is wired up for this controller")
	}

	if err := lock.Renew(ctx, operationLockIdentity(op)); err != nil {
		return renewRefusal(ctx, err)
	}

	// The Secret is read uncached: it decides which CA is trusted, and a stale read after this
	// controller's own write would redo a finished step.
	reader := readerOrClient(r.APIReader, r.Client)

	bundle, err := loadBundle(ctx, reader, r.ControllerNamespace, cluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading secrets bundle to rotate %q's CAs: %w", cluster.Name, err)
	}

	run.bundle = bundle
	run.scrub.add(bundleSecretMaterial(bundle)...)

	rot, err := secretsbundle.LoadRotation(ctx, reader, r.ControllerNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return ctrl.Result{}, run.blocked(ctx, "reading the new CAs failed: "+err.Error())
	}

	if rot != nil {
		run.rot = rot
		// Trust the new Kubernetes CA's certificate (never its key) before the API server switches to it at
		// Issuing, so this controller's own client keeps verifying. A failure only costs the
		// restart this avoids, so it does not stop the rotation.
		if rot.Kubernetes != nil {
			if err := kubetrust.Shared.Publish(rot.Kubernetes.Crt, kubetrust.TrustFile); err != nil {
				ctrl.LoggerFrom(ctx).Error(err, "Could not publish the new Kubernetes CA to this controller's trust pool")
			}
		}

		run.scrub.add(rotationMaterial(rot)...)
	}

	rs := op.Status.CARotation

	switch {
	case rs == nil || rs.Stage == "":
		return run.preflight(ctx)
	case rs.Stage == ichorv1alpha1.CARotationStageFinalizing:
		return run.finalize(ctx)
	case rs.Stage == ichorv1alpha1.CARotationStageDone:
		return run.complete(ctx)
	}

	if rot == nil {
		return ctrl.Result{RequeueAfter: operationRequeueInterval}, run.blocked(ctx, fmt.Sprintf(
			"the Secret %q has no %s although the rotation is in stage %s; the new CAs' keys cannot be found, so "+
				"nothing is advanced. Restore %s from the copy made when the rotation started",
			cluster.Spec.SecretsRef.Name, secretsbundle.RotationKey, rs.Stage, secretsbundle.RotationKey))
	}

	if rs.Stage == ichorv1alpha1.CARotationStageAwaitingCredentials {
		return run.awaitCredentials(ctx)
	}

	return run.pass(ctx, rs.Stage)
}

// status writes the phase and the CARotation condition in one update. Every message goes through
// the scrubber first.
func (c *caRun) status(ctx context.Context, phase string, cond metav1.ConditionStatus, reason, message string) error {
	c.op.Status.Phase = phase
	apimeta.SetStatusCondition(&c.op.Status.Conditions, metav1.Condition{
		Type: conditionCARotation, Status: cond, Reason: reason, Message: c.scrub.clean(message),
	})

	return c.r.Status().Update(ctx, c.op)
}

// blocked pauses the operation without advancing anything; it is retried by the caller's requeue.
func (c *caRun) blocked(ctx context.Context, message string) error {
	return c.status(ctx, ichorv1alpha1.TalosOperationPhasePaused, metav1.ConditionFalse, reasonCARotationBlocked, message)
}

func (c *caRun) retry(ctx context.Context, message string) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: operationRequeueInterval}, c.blocked(ctx, message)
}

// retryWait is retry for the await steps, whose caller must stop and return its result.
func (c *caRun) retryWait(ctx context.Context, message string) (ctrl.Result, bool, error) {
	res, err := c.retry(ctx, message)

	return res, true, err
}

// settle holds a stage for delay after the CARotation condition first turned True with reason: the first call
// starts the clock by writing started, later ones wait out what is left. wait is false once delay has passed. The
// clock is the condition's transition time, so it survives a controller restart.
func (c *caRun) settle(ctx context.Context, reason string, delay time.Duration, started string) (ctrl.Result, bool, error) {
	cond := apimeta.FindStatusCondition(c.op.Status.Conditions, conditionCARotation)
	if cond == nil || cond.Reason != reason || cond.Status != metav1.ConditionTrue {
		// A removed condition gets a fresh transition time.
		apimeta.RemoveStatusCondition(&c.op.Status.Conditions, conditionCARotation)

		err := c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue, reason, started)

		return ctrl.Result{RequeueAfter: min(delay, operationRequeueInterval)}, true, err
	}

	if left := delay - time.Since(cond.LastTransitionTime.Time); left > 0 {
		return ctrl.Result{RequeueAfter: min(left, operationRequeueInterval)}, true, nil
	}

	return ctrl.Result{}, false, nil
}

// machines returns the machines to rotate, control planes first. A rotation reaches every
// approved machine: an upgrade hold excludes a machine from a rollout, but a machine left out here
// would be locked out when the old CA is dropped.
func (c *caRun) machines(ctx context.Context) ([]*ichorv1alpha1.TalosMachine, error) {
	list, err := deleteguard.ApprovedMachines(ctx, readerOrClient(c.r.APIReader, c.r.Client),
		c.cluster.Namespace, c.cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return nil, err
	}

	all := ptrs(list)
	cps := controlPlanes(all)
	workers := slices.DeleteFunc(all, func(m *ichorv1alpha1.TalosMachine) bool { return m.Spec.Role == roleControlPlane })

	upgrade.SortByOrder(cps)
	upgrade.SortByOrder(workers)

	return slices.Concat(cps, workers), nil
}

// withNode dials m with bundle and runs fn.
func (c *caRun) withNode(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, fn func(context.Context, TalosClient) error,
) error {
	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	talos, err := c.r.TalosClient(dialCtx, c.cluster, m, bundle)
	if err != nil {
		return fmt.Errorf("dialing %q: %w", m.Name, err)
	}
	defer closeTalosClient(talos)

	return fn(dialCtx, talos)
}

// dialBundle is the bundle Ichor dials with while two CAs are in play: signer signs Ichor's client
// certificate and both CAs are trusted for the server's, since which one a node serves from
// changes under Ichor's feet during a pass. Only the Talos API CA fields are replaced.
func dialBundle(bundle *secrets.Bundle, signer, other *x509.PEMEncodedCertificateAndKey) *secrets.Bundle {
	trust := slices.Concat(withNewline(signer.Crt), withNewline(other.Crt))

	out := *bundle
	certs := *bundle.Certs
	certs.OS = &x509.PEMEncodedCertificateAndKey{Crt: trust, Key: signer.Key}
	out.Certs = &certs

	return &out
}

func withNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(slices.Clone(b), '\n')
	}

	return b
}

// plainRender renders m without any rotation input.
func (c *caRun) plainRender(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, patches []ichorv1alpha1.TalosConfigPatch,
	rotation *render.CARotation,
) ([]byte, string, error) {
	sys, err := render.ResolveSystemInputs(ctx, c.r.Client, c.r.ControllerNamespace, c.cluster)
	if err != nil {
		return nil, "", err
	}

	sys.CARotation = rotation

	return render.Render(c.cluster, m, bundle, patches, sys)
}

// nodesReady fails unless every Kubernetes Node is Ready, asked through the first control plane's
// admin kubeconfig.
func (c *caRun) nodesReady(ctx context.Context, first *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) error {
	return c.withNode(ctx, first, bundle, c.r.checkNodesReady)
}

// checkNodesReady fails unless every Kubernetes Node is Ready, asked through talos's admin kubeconfig.
func (r *TalosOperationReconciler) checkNodesReady(ctx context.Context, talos TalosClient) error {
	notReady, err := r.NodesReady(ctx, talos)
	if err != nil {
		return fmt.Errorf("checking the Kubernetes Nodes: %w", err)
	}

	if len(notReady) > 0 {
		return fmt.Errorf("kubernetes Nodes not Ready: %v", notReady)
	}

	return nil
}

// preflight refuses to start unless every machine answers, matches what Ichor would render for
// it, and every Node is Ready: a machine that is down when the old CA is dropped is locked out
// for good. Then it saves the new CAs and enters the first stage.
func (c *caRun) preflight(ctx context.Context) (ctrl.Result, error) {
	machines, err := c.machines(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(machines) == 0 {
		return c.retry(ctx, "the cluster has no approved node to rotate")
	}

	// The targets are what rotation.yaml holds when an earlier attempt saved one, else what was asked for.
	targets := c.requestedTargets()
	if c.rot != nil {
		targets = c.rot.Targets()
	}

	if err := c.checkTargets(targets); err != nil {
		return c.retry(ctx, "not starting the rotation: "+err.Error())
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := c.r.List(ctx, &patches, client.InNamespace(c.cluster.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	for _, m := range machines {
		_, hash, err := c.plainRender(ctx, m, c.bundle, patches.Items, nil)
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("rendering %q's configuration failed: %v", m.Name, err))
		}

		err = c.withNode(ctx, m, c.bundle, func(ctx context.Context, talos TalosClient) error {
			if _, err := talos.Version(ctx); err != nil {
				return fmt.Errorf("%q does not answer: %w", m.Name, err)
			}

			current, err := talos.CurrentConfig(ctx)
			if err != nil {
				return fmt.Errorf("reading %q's configuration: %w", m.Name, err)
			}

			if render.Hash(current) != hash {
				return fmt.Errorf("%q does not run the configuration Ichor renders for it; converge it before rotating", m.Name)
			}

			return nil
		})
		if err != nil {
			return c.retry(ctx, "not starting the rotation: "+err.Error())
		}
	}

	if err := c.nodesReady(ctx, machines[0], c.bundle); err != nil {
		return c.retry(ctx, "not starting the rotation: "+err.Error())
	}

	// etcd loses a member for each restart of the roll: it must be whole before the first one.
	if slices.Contains(targets, etcdCAName) {
		if err := c.etcdHealthy(ctx, controlPlanes(machines), c.bundle, nil); err != nil {
			return c.retry(ctx, "not starting the rotation: "+err.Error())
		}
	}

	// Save the new CAs, keys included, before any machine is asked to trust them. A rotation.yaml
	// left by an earlier attempt that never reached a machine is adopted, not replaced.
	if c.rot == nil {
		contract, err := coreconfig.ParseContractFromVersion(c.cluster.Spec.VersionContract)
		if err != nil {
			return c.retry(ctx, "invalid versionContract")
		}

		rot, err := secretsbundle.NewRotationFor(time.Now(), contract, targets)
		if err != nil {
			return ctrl.Result{}, err
		}

		if err := c.saveRotation(ctx, rot); err != nil {
			return ctrl.Result{}, fmt.Errorf("saving the new CAs before rotating: %w", err)
		}

		c.rot = rot
		c.scrub.add(rotationMaterial(rot)...)
	}

	certs, err := c.initialCertificates()
	if err != nil {
		return ctrl.Result{}, err
	}

	c.op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{
		Stage: ichorv1alpha1.CARotationStageAccepting, Certificates: certs,
	}
	setStageCertificates(c.op.Status.CARotation)

	return ctrl.Result{RequeueAfter: caRotationNextStage}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
		reasonCARotationSaved, "the new CAs are saved; adding them to every node's accepted CAs")
}

// bundleKey is the only Secret a rotation ever writes: the one the cluster's spec.secretsRef names, in the
// controller namespace. The manager's Role grants update on every Secret there (RBAC cannot filter by a name the
// user chooses), so this and updateBundle are what keep the write to that one object.
func (r *TalosOperationReconciler) bundleKey(cluster *ichorv1alpha1.TalosCluster) types.NamespacedName {
	return types.NamespacedName{Namespace: r.ControllerNamespace, Name: cluster.Spec.SecretsRef.Name}
}

// updateBundle refuses to write any Secret other than bundleKey.
func (r *TalosOperationReconciler) updateBundle(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, secret *corev1.Secret) error {
	if secret.Name == "" || client.ObjectKeyFromObject(secret) != r.bundleKey(cluster) {
		return fmt.Errorf("refusing to write Secret %s/%s: it is not the secrets bundle %q names", secret.Namespace, secret.Name, cluster.Name)
	}

	return r.Update(ctx, secret)
}

// saveRotation writes rotation.yaml into the bundle Secret, once and only if absent.
func (c *caRun) saveRotation(ctx context.Context, rot *secretsbundle.Rotation) error {
	raw, err := rot.Marshal()
	if err != nil {
		return errors.New("encoding the new CAs")
	}

	var secret corev1.Secret

	key := c.r.bundleKey(c.cluster)
	if err := readerOrClient(c.r.APIReader, c.r.Client).Get(ctx, key, &secret); err != nil {
		return err
	}

	if _, exists := secret.Data[secretsbundle.RotationKey]; exists {
		return errors.New("rotation.yaml appeared while the new CAs were being generated")
	}

	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}

	secret.Data[secretsbundle.RotationKey] = raw

	return c.r.updateBundle(ctx, c.cluster, &secret)
}

func (c *caRun) initialCertificates() ([]ichorv1alpha1.CARotationCertificate, error) {
	var out []ichorv1alpha1.CARotationCertificate

	pairs, err := c.pairs()
	if err != nil {
		return nil, err
	}

	for _, p := range pairs {
		prev, err := secretsbundle.Fingerprint(p.old)
		if err != nil {
			return nil, fmt.Errorf("%s CA: %w", p.name, err)
		}

		next, err := secretsbundle.Fingerprint(p.next)
		if err != nil {
			return nil, fmt.Errorf("%s CA: %w", p.name, err)
		}

		out = append(out, ichorv1alpha1.CARotationCertificate{Name: p.name, Previous: prev, Next: next})
	}

	return out, nil
}

// setStageCertificates writes, from rs.Stage, which CA issues and which are accepted in it.
func setStageCertificates(rs *ichorv1alpha1.TalosOperationCARotationStatus) {
	for i := range rs.Certificates {
		cert := &rs.Certificates[i]

		switch rs.Stage {
		case ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials:
			cert.Issuing, cert.Accepted = cert.Previous, []string{cert.Next}
		case ichorv1alpha1.CARotationStageIssuing:
			cert.Issuing, cert.Accepted = cert.Next, []string{cert.Previous}
		default:
			cert.Issuing, cert.Accepted = cert.Next, nil
		}
	}
}

// pass makes every machine run the configuration of stage, then verifies it. Applying and
// verifying are separate reconciles: a stage that applied anything is checked on the next one, so
// the kubelet restart a CA change causes has begun before Node readiness is read.
func (c *caRun) pass(ctx context.Context, stage string) (ctrl.Result, error) {
	machines, err := c.machines(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(machines) == 0 {
		return c.retry(ctx, "the cluster has no approved node to rotate")
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := c.r.List(ctx, &patches, client.InNamespace(c.cluster.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	// Accepting is applied with the old client certificate, which every machine still accepts;
	// every later stage with the new one, which the Accepting pass proved they accept.
	// A rotation without the Talos API CA (etcd, aggregator) dials with the bundle as it is.
	applyBundle, verifyBundle := c.bundle, c.bundle

	if c.rot.Talos != nil {
		applySigner, applyOther := c.rot.Talos, c.bundle.Certs.OS
		if stage == ichorv1alpha1.CARotationStageAccepting {
			applySigner, applyOther = c.bundle.Certs.OS, c.rot.Talos
		}

		applyBundle = dialBundle(c.bundle, applySigner, applyOther)
		verifyBundle = dialBundle(c.bundle, c.rot.Talos, c.bundle.Certs.OS)
	}

	rotation := &render.CARotation{Stage: stage, New: c.rot}

	// A control plane that is rebooting cannot be dialed to read its configuration: while one is, the pass is
	// the wait for it, not another round of applies (the roll starts only when every machine runs the stage).
	if c.op.Status.CARotation.EtcdRebooting != "" {
		if res, wait, err := c.rollEtcd(ctx, stage, machines, verifyBundle); wait {
			return res, err
		}
	}

	applied := 0

	for _, m := range machines {
		rendered, hash, err := c.plainRender(ctx, m, c.bundle, patches.Items, rotation)
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("rendering %q's %s configuration failed: %v", m.Name, stage, err))
		}

		didApply := false

		err = c.withNode(ctx, m, applyBundle, func(dialCtx context.Context, talos TalosClient) error {
			current, err := talos.CurrentConfig(dialCtx)
			if err != nil {
				return fmt.Errorf("reading %q's configuration: %w", m.Name, err)
			}

			if render.Hash(current) == hash {
				return nil
			}

			if _, err := talos.DryRunApply(dialCtx, rendered); err != nil {
				return fmt.Errorf("dry-run apply to %q failed: %w", m.Name, err)
			}

			baseline, err := c.apiServerBaseline(dialCtx, talos, m)
			if err != nil {
				return err
			}

			// The apply gets its own deadline: the 15s dial bound would cut it off while Talos
			// restarts services (the Kubernetes upgrade hit exactly that).
			applyCtx, cancelApply := context.WithTimeout(ctx, upgradeCallTimeout)
			defer cancelApply()

			if err := talos.ApplyNoReboot(applyCtx, rendered); err != nil {
				return fmt.Errorf("applying the %s configuration to %q failed: %w", stage, m.Name, err)
			}

			c.recordAPIServerBaseline(m, baseline)

			didApply = true

			return nil
		})
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("stage %s did not advance: %v", stage, err))
		}

		if didApply {
			applied++

			c.r.markMachine(c.op, m.Name, ichorv1alpha1.TalosOperationMachinePhaseInProgress)
		}
	}

	if applied > 0 {
		now := metav1.Now()
		c.op.Status.CARotation.AppliedAt = &now

		return ctrl.Result{RequeueAfter: operationRequeueInterval}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress,
			metav1.ConditionTrue, reasonCARotationStage,
			fmt.Sprintf("stage %s: applied to %d of %d node(s); verifying on the next pass", stage, applied, len(machines)))
	}

	// Every machine runs the stage's configuration. etcd loads its CA field only at start and Talos cannot restart
	// it, so the control planes are rebooted now, one at a time.
	if res, wait, err := c.rollEtcd(ctx, stage, machines, verifyBundle); wait {
		return res, err
	}

	// Verify Ichor can still reach each one with the credentials of the stage, and that Kubernetes is
	// healthy, before moving on.
	for _, m := range machines {
		err := c.withNode(ctx, m, verifyBundle, func(ctx context.Context, talos TalosClient) error {
			if _, err := talos.Version(ctx); err != nil {
				return fmt.Errorf("ichor cannot reach %q with the %s credentials: %w", m.Name, stage, err)
			}

			return nil
		})
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("stage %s is not verified, so the next stage is not started: %v", stage, err))
		}
	}

	if res, wait, err := c.awaitAPIServers(ctx, stage, machines, verifyBundle); wait {
		return res, err
	}

	if err := c.nodesReady(ctx, machines[0], verifyBundle); err != nil {
		return c.retry(ctx, fmt.Sprintf("stage %s is not verified, so the next stage is not started: %v", stage, err))
	}

	if stage == ichorv1alpha1.CARotationStageIssuing && c.hasTarget(kubernetesCAName) {
		if res, wait, err := c.awaitClients(ctx, machines[0], verifyBundle); wait {
			return res, err
		}
	}

	if stage == ichorv1alpha1.CARotationStageAccepting {
		if res, wait, err := c.awaitAggregator(ctx, machines[0], verifyBundle); wait {
			return res, err
		}
	}

	if err := c.dropEarlyHandoff(ctx, stage); err != nil {
		return ctrl.Result{}, err
	}

	// status.machines shows the stage under way: the next one starts empty.
	c.op.Status.Machines = nil
	c.op.Status.CARotation.EtcdRestarted, c.op.Status.CARotation.EtcdRestartedAt = nil, nil
	c.op.Status.CARotation.EtcdRebooting, c.op.Status.CARotation.EtcdRebootBootID = "", ""
	c.op.Status.CARotation.APIServerInstances, c.op.Status.CARotation.AppliedAt = nil, nil
	c.op.Status.CARotation.Stage = c.nextStage(stage)
	setStageCertificates(c.op.Status.CARotation)

	return ctrl.Result{RequeueAfter: caRotationNextStage}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue,
		reasonCARotationVerifying, fmt.Sprintf("stage %s verified on %d node(s)", stage, len(machines)))
}

// dropEarlyHandoff removes the credentials hand-off annotation when the verified stage is the one that
// leads into AwaitingCredentials. The token is made of fingerprints visible from Accepting on, so an
// acknowledgement set before the operation waits for it would match the moment it does and skip the gate; one set
// after it is honoured. The patch goes through a copy (its response would
// replace c.op's status) and only the resource version is taken over, so a change of the annotation after
// the drop fails the status update that follows and the next pass drops it.
func (c *caRun) dropEarlyHandoff(ctx context.Context, stage string) error {
	if _, early := c.op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation]; !early ||
		c.nextStage(stage) != ichorv1alpha1.CARotationStageAwaitingCredentials {
		return nil
	}

	patched := c.op.DeepCopy()
	drop := fmt.Sprintf(`{"metadata":{"annotations":{%q:null}}}`, ichorv1alpha1.CredentialsHandoffAnnotation)

	if err := c.r.Patch(ctx, patched, client.RawPatch(types.MergePatchType, []byte(drop))); err != nil {
		return fmt.Errorf("dropping an acknowledgement given before the credentials were awaited: %w", err)
	}

	delete(c.op.Annotations, ichorv1alpha1.CredentialsHandoffAnnotation)
	c.op.ResourceVersion = patched.ResourceVersion

	return nil
}

// awaitClients holds the rotation between Issuing and Dropping until in-cluster clients can have
// picked up the Kubernetes API's new serving CA. Talos hands kube-controller-manager only the
// issuing CA as --root-ca-file, so kube-root-ca.crt (the source of every pod's projected ca.crt)
// holds the old CA until Issuing and only the new one after: there is no moment with both, and
// pods that read ca.crt once distrust the API server until the file is refreshed (kubelet needs
// about a minute; client-go re-reads it at most every 5 minutes). Ichor cannot close that gap, but
// it does not remove the old CA from the nodes until kube-root-ca.crt carries the new one
// everywhere and the kubelets have had caClientSettleDelay to project it. wait is false when the
// rotation may go on. The settle clock is the CARotation condition's transition time, so it
// survives a controller restart.
func (c *caRun) awaitClients(ctx context.Context, first *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) (ctrl.Result, bool, error) {
	if c.r.RootCAPublished == nil {
		return ctrl.Result{}, false, nil
	}

	var stale []string

	err := c.withNode(ctx, first, bundle, func(ctx context.Context, talos TalosClient) error {
		var err error

		stale, err = c.r.RootCAPublished(ctx, talos, c.newFingerprint(kubernetesCAName))

		return err
	})
	if err != nil {
		return c.retryWait(ctx, "stage Issuing is not settled, so the old CAs stay: could not read kube-root-ca.crt: "+err.Error())
	}

	if len(stale) > 0 {
		shown := stale[:min(len(stale), 5)]
		return c.retryWait(ctx, fmt.Sprintf("stage Issuing is not settled, so the old CAs stay: waiting for kube-root-ca.crt to carry "+
			"the new Kubernetes CA in %d namespace(s), among them %v; pods there do not trust the API server yet", len(stale), shown))
	}

	delay := cmp.Or(c.r.ClientSettleDelay, caClientSettleDelay)

	return c.settle(ctx, reasonCARotationSettling, delay, fmt.Sprintf(
		"kube-root-ca.crt carries the new Kubernetes CA everywhere; giving pods %s to load it before the old CAs are dropped. "+
			"In-cluster clients that read the CA only once may need a restart", delay))
}

// awaitCredentials holds the rotation, changing nothing, until the operator has acknowledged the
// credential hand-off: from the next stage on the Kubernetes API serves the new CA and,
// two stages later, the Talos API stops accepting the old one, so a kubeconfig or talosconfig that
// trusts only the old CAs would be locked out. The annotation is set by the CLI after it has written
// credentials valid under both CAs; the controller only reads it, and only when it names this
// rotation's new CAs.
func (c *caRun) awaitCredentials(ctx context.Context) (ctrl.Result, error) {
	want := ichorv1alpha1.CARotationHandoffToken(c.op.Status.CARotation)
	if want != "" && c.op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation] == want {
		c.op.Status.CARotation.Stage = c.nextStage(c.op.Status.CARotation.Stage)
		setStageCertificates(c.op.Status.CARotation)

		return ctrl.Result{RequeueAfter: caRotationNextStage}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress,
			metav1.ConditionTrue, reasonCARotationVerifying, "the operator has new credentials; making the new CAs issue")
	}

	return ctrl.Result{RequeueAfter: operationRequeueInterval}, c.status(ctx, ichorv1alpha1.TalosOperationPhasePaused,
		metav1.ConditionFalse, reasonCARotationAwaitingCredentials, fmt.Sprintf(
			"every node accepts the new CAs and the old ones still work. Nothing else happens until you have credentials that "+
				"work after the next stage: run `ichor create credentials %s --acknowledge` "+
				"with your current kubeconfig, or download them and continue on the operation page of the UI", c.cluster.Name))
}

// finalize replaces secrets.yaml with the new CAs and removes rotation.yaml in one Secret update,
// raises BundleBackupStale, and checks Ichor's dial and every machine against the replaced bundle.
func (c *caRun) finalize(ctx context.Context) (ctrl.Result, error) {
	bundle := c.bundle

	if c.rot != nil {
		replaced, err := c.replaceBundle(ctx)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("replacing the secrets bundle: %w", err)
		}

		bundle = replaced
	} else if !c.holdsNewCAs(bundle) {
		return c.retry(ctx, "the secrets bundle neither carries the new CAs nor has a rotation.yaml to take them from")
	}

	if err := c.markBundleBackupStale(ctx); err != nil {
		return ctrl.Result{}, err
	}

	machines, err := c.machines(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := c.r.List(ctx, &patches, client.InNamespace(c.cluster.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	for _, m := range machines {
		_, hash, err := c.plainRender(ctx, m, bundle, patches.Items, nil)
		if err != nil {
			return c.retry(ctx, fmt.Sprintf("rendering %q's configuration failed: %v", m.Name, err))
		}

		err = c.withNode(ctx, m, bundle, func(ctx context.Context, talos TalosClient) error {
			current, err := talos.CurrentConfig(ctx)
			if err != nil {
				return fmt.Errorf("ichor cannot reach %q with the replaced bundle: %w", m.Name, err)
			}

			if render.Hash(current) != hash {
				return fmt.Errorf("%q does not run the configuration the replaced bundle renders", m.Name)
			}

			return nil
		})
		if err != nil {
			return c.retry(ctx, "the replaced bundle is not verified: "+err.Error())
		}
	}

	// kube-apiserver never removes the old aggregator CA from the ConfigMap extension API servers read: ask it to
	// recreate that ConfigMap, and finish only when it holds the new CA alone.
	if len(machines) > 0 {
		if res, wait, err := c.awaitAggregatorAuth(ctx, machines[0], bundle); wait {
			return res, err
		}
	}

	// The kubelets and the in-cluster clients depend on the Kubernetes API CA only. A rotation without it
	// (Talos API, etcd, aggregator) needs the Nodes Ready and nothing restarted.
	switch {
	case !c.hasTarget(kubernetesCAName):
		if len(machines) > 0 {
			if err := c.nodesReady(ctx, machines[0], bundle); err != nil {
				return c.retry(ctx, "the replaced bundle is not verified: "+err.Error())
			}
		}
	default:
		if len(machines) > 0 {
			if res, wait, err := c.awaitNodes(ctx, machines[0], bundle); wait {
				return res, err
			}
		}

		res, note, done, err := c.restartClients(ctx)
		if !done {
			return res, err
		}

		c.restartNote = note
	}

	// Done is the last thing this reconcile verified, so the operation completes now: a requeue here
	// would leave it InProgress at Done for as long as the requeue takes to come back.
	c.op.Status.CARotation.Stage = ichorv1alpha1.CARotationStageDone
	setStageCertificates(c.op.Status.CARotation)

	return c.complete(ctx)
}

// awaitNodes holds the first client restart until the Nodes are Ready again after the Dropping stage.
// The kubelets restart then, and a Node keeps reporting Ready=True until the controller manager's
// grace period (40 s) passes without a heartbeat, so a single Ready read taken just after Dropping is
// stale: the restarts began while the kubelets were going down and CoreDNS lost its replicas. The
// signal is therefore a live read (NodesReady asks the API through a freshly issued admin kubeconfig,
// never a cache) that stays Ready for caKubeletSettleDelay, which is longer than the grace period plus
// a kubelet restart: a Node that is going to turn NotReady has done so within it, and that resets the
// clock. Node condition times are not used because NodesReadyFunc reports only names, and a kubelet
// that restarts inside the grace period never changes lastTransitionTime. The clock is the CARotation
// condition's transition time, so it survives a controller restart. Until then the operation waits
// (InProgress with the reason), it is not Paused. Once the restarts have begun the plain check applies.
func (c *caRun) awaitNodes(ctx context.Context, first *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle) (ctrl.Result, bool, error) {
	cond := apimeta.FindStatusCondition(c.op.Status.Conditions, conditionCARotation)
	restarting := cond != nil && cond.Reason == reasonCARotationRestarting && cond.Status == metav1.ConditionTrue

	if c.r.KubeClient == nil || restarting {
		if err := c.nodesReady(ctx, first, bundle); err != nil {
			return c.retryWait(ctx, "the replaced bundle is not verified: "+err.Error())
		}

		return ctrl.Result{}, false, nil
	}

	if err := c.nodesReady(ctx, first, bundle); err != nil {
		// Reset the clock: a removed condition gets a fresh transition time.
		apimeta.RemoveStatusCondition(&c.op.Status.Conditions, conditionCARotation)

		err := c.status(ctx, ichorv1alpha1.TalosOperationPhaseInProgress, metav1.ConditionTrue, reasonCARotationAwaitingNodes,
			"the old CAs are dropped; no client is restarted until the Nodes are Ready again after the kubelets restarted: "+err.Error())

		return ctrl.Result{RequeueAfter: operationRequeueInterval}, true, err
	}

	return c.settle(ctx, reasonCARotationSettlingNodes, caKubeletSettleDelay, fmt.Sprintf(
		"the old CAs are dropped and the Nodes are Ready; waiting %s to see they stay Ready after the kubelets restarted, "+
			"before any client is restarted", caKubeletSettleDelay))
}

// holdsNewCAs reports whether bundle already carries the CAs this rotation is moving to.
func (c *caRun) holdsNewCAs(bundle *secrets.Bundle) bool {
	for _, cert := range c.op.Status.CARotation.Certificates {
		if fp, err := secretsbundle.Fingerprint(bundleCrt(bundle, cert.Name)); err != nil || fp != cert.Next {
			return false
		}
	}

	return true
}

// replaceBundle writes the new CAs into secrets.yaml and removes rotation.yaml, in one update that
// fails if anyone changed the Secret meanwhile.
func (c *caRun) replaceBundle(ctx context.Context) (*secrets.Bundle, error) {
	replaced := c.rot.Apply(c.bundle)

	raw, err := yaml.Marshal(replaced)
	if err != nil {
		return nil, errors.New("encoding the replaced bundle")
	}

	var secret corev1.Secret

	key := c.r.bundleKey(c.cluster)
	if err := readerOrClient(c.r.APIReader, c.r.Client).Get(ctx, key, &secret); err != nil {
		return nil, err
	}

	secret.Data[secretsbundle.SecretKey] = raw
	delete(secret.Data, secretsbundle.RotationKey)

	if err := c.r.updateBundle(ctx, c.cluster, &secret); err != nil {
		return nil, err
	}

	return replaced, nil
}

// markBundleBackupStale raises BundleBackupStale. The message carries no secret; it names the command that clears it.
func (c *caRun) markBundleBackupStale(ctx context.Context) error {
	return c.r.raiseBundleBackupStale(ctx, c.cluster, bundleStaleReason, fmt.Sprintf(
		"A CA rotation replaced the secrets bundle. The copy saved on your machine no longer matches the "+
			"cluster, and recovering from a total loss needs the new one. Run `ichor export bundle %s` and keep the file safe.",
		c.cluster.Name))
}

// raiseBundleBackupStale raises BundleBackupStale on the cluster, and an Event the first time.
func (r *TalosOperationReconciler) raiseBundleBackupStale(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, reason, message string,
) error {
	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionBundleBackupStale) {
		return nil
	}

	base := cluster.DeepCopy()
	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionBundleBackupStale, Status: metav1.ConditionTrue,
		Reason: reason, Message: message, ObservedGeneration: cluster.Generation,
	})

	if err := r.Status().Patch(ctx, cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("raising BundleBackupStale: %w", err)
	}

	r.operationEvent(cluster, corev1.EventTypeWarning, ichorv1alpha1.ConditionBundleBackupStale, message)

	return nil
}

// complete ends the operation: the lock is released, the phase is Completed.
func (c *caRun) complete(ctx context.Context) (ctrl.Result, error) {
	if err := c.lock.Release(ctx, operationLockIdentity(c.op)); err != nil {
		return ctrl.Result{}, err
	}

	// The clients are restarted and kube-root-ca.crt carries the new CA, so this controller's projected
	// ca.crt alone should do. If it has not refreshed yet the extras stay and CATrustSync clears later.
	if _, err := kubetrust.Shared.ClearOnceBaseTrusts(kubetrust.TrustFile); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Could not clear this controller's extra trusted CAs")
	}

	reason, message := reasonCARotationComplete, "the "+c.replacedCAs()+" CAs are replaced; export the new secrets bundle"+c.restartNote

	if c.hasTarget(aggregatorCAName) {
		message += "; an extension API server that read the aggregator CA only once needs a restart by its owner"
	}

	if c.hasTarget(kubernetesCAName) && c.cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		message += "; the CNI is unmanaged, so Ichor did not restart it: restart its pods if they report x509 errors"
	}

	if c.restartNote != "" {
		reason = reasonCARotationClientsNotBack
	}

	if err := c.setClientsNotRestarted(ctx, c.restartNote != "", message); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, c.status(ctx, ichorv1alpha1.TalosOperationPhaseCompleted, metav1.ConditionFalse, reason, message)
}

// setClientsNotRestarted raises (notBack) or lowers the cluster's CAClientsNotRestarted condition. The
// operation still ends Completed (the CAs are replaced), but with a distinct reason, this condition,
// which the UI lists under Needs attention, and a Warning Event on the raise: it is not a clean success.
func (c *caRun) setClientsNotRestarted(ctx context.Context, notBack bool, message string) error {
	if !notBack && !apimeta.IsStatusConditionTrue(c.cluster.Status.Conditions, ichorv1alpha1.ConditionCAClientsNotRestarted) {
		return nil
	}

	cond := metav1.Condition{
		Type: ichorv1alpha1.ConditionCAClientsNotRestarted, Status: metav1.ConditionFalse, Reason: "Restarted",
		Message: "the clients restarted after the last CA rotation are back", ObservedGeneration: c.cluster.Generation,
	}
	if notBack {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionTrue, bundleStaleReason, c.scrub.clean(message)
	}

	base := c.cluster.DeepCopy()
	apimeta.SetStatusCondition(&c.cluster.Status.Conditions, cond)

	if err := c.r.Status().Patch(ctx, c.cluster, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("setting CAClientsNotRestarted: %w", err)
	}

	if notBack {
		c.r.operationEvent(c.cluster, corev1.EventTypeWarning, ichorv1alpha1.ConditionCAClientsNotRestarted, cond.Message)
	}

	return nil
}
