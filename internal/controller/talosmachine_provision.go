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
	"io"
	"slices"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

// Provisioning is the first-configuration path of an approved machine: a node still in
// maintenance mode does not trust the bundle's CA, so the authenticated client cannot reach it.
// Ichor then renders the configuration as usual, applies it over the insecure maintenance API
// and follows the node's stage until it runs (docs/adr/0076, architecture.md "Nodes").

// conditionProvisioned is False with the reason of the stage the node is in until it has joined,
// then True with reasonNodeJoined.
const conditionProvisioned = "Provisioned"

const (
	reasonIdentityMismatch   = "IdentityMismatch"
	reasonEndpointRefused    = "EndpointRefused"
	reasonMaintenanceChanged = "MaintenanceIdentityChanged"
	reasonInstallDiskUnknown = "InstallDiskUnknown"
	reasonConfigStored       = "ConfigStored"
	reasonAwaitingInstall    = "AwaitingInstall"
	reasonNodeInstalling     = "NodeInstalling"
	reasonNodeRebooting      = "NodeRebooting"
	reasonNodeStarting       = "NodeStarting"
	reasonNodeJoined         = "NodeJoined"
	reasonSecureBootRequired = "SecureBootRequired"
)

// Stage values recorded in TalosMachineStatus.Stage (Talos's MachineStatus stage names).
const (
	stageMaintenance = "maintenance"
	stageInstalling  = "installing"
	stageRebooting   = "rebooting"
	stageRunning     = "running"
)

const (
	// talosCallTimeout bounds each short Talos read; maintenanceApplyTimeout the insecure apply,
	// which only stores the configuration. The install itself takes minutes and is followed by
	// requeue (provisionRecheck), never by a long blocking call.
	talosCallTimeout        = 20 * time.Second
	maintenanceApplyTimeout = 60 * time.Second
	provisionRecheck        = 15 * time.Second
)

// provision handles an approved machine that has never converged and that the authenticated
// client could not read. It decides "in maintenance" by asking the insecure API for the node's
// identity: only a node actually in maintenance mode answers that, so no error text is parsed.
func (r *TalosMachineReconciler) provision(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, rendered []byte, hash string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Defence in depth behind the admission webhook (ADR 0108): the rendered configuration carries
	// the cluster's CA keys, so it is never sent to the controller's own network position.
	if err := endpointguard.ForbiddenResolved(ctx, m.DialEndpoint(), cluster); err != nil {
		return r.retryLater(ctx, m, reasonEndpointRefused,
			fmt.Sprintf("the endpoint %s is not a valid target for this cluster's configuration: %v; nothing was dialled or applied", m.DialEndpoint(), err))
	}

	// The first configuration carries the CA keys, so it only goes to the node whose maintenance
	// certificate was pinned at discovery (ADR 0108). Without a pin the first handshake is trusted
	// and recorded below.
	want := m.Status.MaintenanceFingerprint
	if v := m.Annotations[ichorv1alpha1.TalosMachineTrustMaintenanceFingerprintAnnotation]; v != "" {
		want = v
	}

	pin, perr := NewMaintenancePin(want)
	if perr != nil {
		return r.retryLater(ctx, m, reasonMaintenanceChanged, perr.Error())
	}

	hw, mc, err := r.probeMaintenance(WithMaintenancePin(ctx, pin), m.DialEndpoint())
	if err != nil {
		if IsMaintenancePinMismatch(err) && m.Status.ConfigStoredAt == nil {
			return r.retryLater(ctx, m, reasonMaintenanceChanged,
				fmt.Sprintf("the node at %s presents a different maintenance certificate (%s) than the one pinned (%s), so the "+
					"configuration, which carries the cluster's CA keys, was not sent; if you have checked that this is the "+
					"node, accept the new fingerprint with the %s annotation",
					m.DialEndpoint(), pin.Seen(), want, ichorv1alpha1.TalosMachineTrustMaintenanceFingerprintAnnotation))
		}

		log.V(1).Info("node is not answering the maintenance API", "error", err.Error())

		if m.Status.ConfigStoredAt != nil {
			// Stored, and now neither API answers: it is installing or rebooting.
			m.Status.Stage = stageRebooting

			return r.following(ctx, m, reasonNodeRebooting,
				"the node accepted its configuration and is not answering while it installs and reboots")
		}

		return r.retryLater(ctx, m, reasonCurrentConfigUnavailable,
			"the node answers neither with the cluster's credentials nor in maintenance mode")
	}

	if closer, ok := mc.(io.Closer); ok {
		defer closer.Close() //nolint:errcheck
	}

	if pf, ok := mc.(PeerFingerprinter); ok && want == "" && pf.PeerFingerprint() != "" {
		m.Status.MaintenanceFingerprint = pf.PeerFingerprint()
	}

	// Answering insecurely means maintenance mode. After a store that is the stage before the
	// node begins to install; it is never a reason to apply again.
	m.Status.Stage = stageMaintenance

	if m.Status.ConfigStoredAt != nil {
		return r.following(ctx, m, reasonAwaitingInstall,
			"the configuration was stored but the node is still in maintenance mode; waiting for it to start installing")
	}

	if !identityMatches(m.Spec.Identity, hw) {
		return r.retryLater(ctx, m, reasonIdentityMismatch,
			fmt.Sprintf("the node at %s reports UUID %q, serial %q and MAC addresses %v, which do not match this machine's "+
				"recorded identity; nothing was applied", m.Spec.Endpoint, hw.UUID, hw.Serial, hw.MACAddresses))
	}

	// SecureBoot Required (ADR 0123): the controller cannot make a node boot with SecureBoot, but it
	// can decline to install onto one that did not, before the first configuration is sent.
	if res, stop, err := r.requireSecureBoot(ctx, cluster, m, mc); stop {
		return res, err
	}

	known, err := installDiskKnown(rendered)
	if err != nil {
		return r.retryLater(ctx, m, reasonRenderFailed, "the rendered configuration could not be inspected for an install disk")
	}

	if !known {
		return r.retryLater(ctx, m, reasonInstallDiskUnknown,
			"the rendered configuration names no install disk (machine.install.disk or diskSelector); "+
				"a node in maintenance mode cannot install without one, so nothing was applied")
	}

	// A control plane joining changes etcd membership: take the cluster lock like any disruptive
	// operation. completesTransition is true because a join is exactly what moves an even count
	// (the replacement in progress) back to odd; refusing it would deadlock the replacement.
	// The over-cap and CA-rotation holds already ran in Reconcile.
	if m.Spec.Role == roleControlPlane {
		lock, err := r.acquireDisruptiveLock(ctx, cluster, m, machineLockIdentity(m), true)
		if err != nil {
			return r.lockRefusal(ctx, m, err, "", "control plane join deferred")
		}

		defer func() {
			if err := lock.Release(ctx, machineLockIdentity(m)); err != nil {
				log.Error(err, "releasing cluster lock")
			}
		}()
	}

	applyCtx, cancel := context.WithTimeout(ctx, maintenanceApplyTimeout)
	defer cancel()

	if err := mc.ApplyConfiguration(applyCtx, rendered); err != nil {
		log.Error(err, "applying initial configuration in maintenance mode")

		return r.retryLater(ctx, m, reasonApplyFailed, "the node refused or did not answer the initial configuration")
	}

	now := metav1.Now()
	m.Status.ConfigStoredAt = &now
	m.Status.Stage = stageInstalling

	r.event(m, corev1.EventTypeNormal, "ConfigApplied",
		"initial configuration "+hash[:12]+" stored on the node in maintenance mode; following it through install and reboot")

	return r.following(ctx, m, reasonConfigStored,
		"the node stored its configuration (this only means stored); waiting for install, reboot and join")
}

// probeMaintenance dials the insecure API and reads the node's identity, each under its own
// timeout. The connection is returned open for the caller to apply over.
func (r *TalosMachineReconciler) probeMaintenance(ctx context.Context, endpoint string) (HardwareIdentity, MaintenanceClient, error) {
	dialCtx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	defer cancel()

	mc, err := r.MaintenanceClient(dialCtx, endpoint)
	if err != nil {
		return HardwareIdentity{}, nil, err
	}

	hw, err := mc.Identity(dialCtx)
	if err != nil {
		if closer, ok := mc.(io.Closer); ok {
			closer.Close() //nolint:errcheck
		}

		return HardwareIdentity{}, nil, err
	}

	return hw, mc, nil
}

// following records Ready=False and Provisioned=False with the stage's reason and asks to be
// reconciled again soon.
func (r *TalosMachineReconciler) following(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, reason, message string,
) (ctrl.Result, error) {
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionProvisioned, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	if err := r.ready(ctx, m, metav1.ConditionFalse, reason, message); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: provisionRecheck}, nil
}

// followStage runs once the authenticated client can read the node. It records the node's stage
// and, while Ichor is still following a node it provisioned, holds the reconcile until the node
// is running; when it is, it records the join. A client that cannot report a stage, or a failed
// read, leaves everything to the ordinary path.
func (r *TalosMachineReconciler) followStage(
	ctx context.Context, talos TalosClient, m *ichorv1alpha1.TalosMachine,
) (res ctrl.Result, stop bool, err error) {
	stageCtx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	stage, serr := talos.MachineStage(stageCtx)

	cancel()

	if serr != nil || stage == "" {
		return ctrl.Result{}, false, nil
	}

	m.Status.Stage = stage

	joined := apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionProvisioned)
	if m.Status.ConfigStoredAt == nil || joined {
		return ctrl.Result{}, false, nil
	}

	if stage == stageRunning {
		apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
			Type: conditionProvisioned, Status: metav1.ConditionTrue, Reason: reasonNodeJoined,
			Message: "the node installed Talos and is running with the cluster's configuration",
		})
		r.event(m, corev1.EventTypeNormal, "NodeJoined", "the node finished installing and is running")

		return ctrl.Result{}, false, nil
	}

	reason := reasonNodeStarting
	switch stage {
	case stageInstalling:
		reason = reasonNodeInstalling
	case stageRebooting:
		reason = reasonNodeRebooting
	}

	message := fmt.Sprintf("the node is in stage %q on its way to running", stage)
	if failed := volumeFailures(ctx, talos); failed != "" {
		message += "; a volume failed: " + failed
	}

	res, err = r.following(ctx, m, reason, message)

	return res, true, err
}

// event records an Event on m when a Recorder is wired.
func (r *TalosMachineReconciler) event(m *ichorv1alpha1.TalosMachine, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(m, eventType, reason, message)
	}
}

// identityMatches reports whether the node found at the endpoint is the machine recorded in
// spec: its UUID must match, and its serial and MAC set must match wherever spec records them.
// A weak spec identity (no serial or MACs, registered by an operator) is compared on what it has.
func identityMatches(spec ichorv1alpha1.TalosMachineIdentity, hw HardwareIdentity) bool {
	if spec.UUID != hw.UUID {
		return false
	}

	if spec.Serial != "" && spec.Serial != hw.Serial {
		return false
	}

	if len(spec.MACAddresses) > 0 {
		a, b := slices.Clone(spec.MACAddresses), slices.Clone(hw.MACAddresses)
		slices.Sort(a)
		slices.Sort(b)

		return slices.Equal(a, b)
	}

	return true
}

// installDiskKnown reports whether the rendered configuration says where to install: a disk, a
// diskSelector or (v1.14) an UnattendedInstallConfig document. Talos refuses a maintenance-mode apply without one.
func installDiskKnown(rendered []byte) (bool, error) {
	provider, err := configloader.NewFromBytes(rendered)
	if err != nil {
		return false, err
	}

	// At contract v1.14 the install target is the UnattendedInstallConfig document; its volume
	// selector is mandatory, so the document being present is enough.
	if provider.UnattendedInstallConfig() != nil {
		return true, nil
	}

	machine := provider.Machine()
	if machine == nil || machine.Install() == nil {
		return false, nil
	}

	if machine.Install().Disk() != "" {
		return true, nil
	}

	expr, err := machine.Install().DiskMatchExpression()

	return expr != nil, err
}

// volumeFailureReader is implemented by a TalosClient that can say which Talos volumes failed.
// Optional on purpose: a client without it leaves the stage message as it was.
type volumeFailureReader interface {
	// VolumeFailures lists the volumes in phase failed with Talos's error message
	// ("STATE: stat /dev/tpm0: no such file or directory"), joined by "; "; "" when none failed.
	VolumeFailures(ctx context.Context) (string, error)
}

// volumeFailures asks talos for failed volumes; a client that cannot, or an error, gives "".
func volumeFailures(ctx context.Context, talos TalosClient) string {
	reader, ok := talos.(volumeFailureReader)
	if !ok {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, talosCallTimeout)
	defer cancel()

	failed, err := reader.VolumeFailures(ctx)
	if err != nil {
		return ""
	}

	return failed
}
