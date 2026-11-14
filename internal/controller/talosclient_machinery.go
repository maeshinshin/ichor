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
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/siderolabs/talos/pkg/machinery/api/common"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	k8sres "github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	runtimeres "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// bootIDPath is Linux's per-boot random ID, the file talosctl reads to confirm a reboot happened.
const bootIDPath = "/proc/sys/kernel/random/boot_id"

// NewMachineryTalosClient dials m's Talos API with the bundle's os:admin identity and adapts the connection
// to TalosClient (wired into TalosMachineReconciler.TalosClient by cmd/main.go). It works once the node
// trusts the bundle's CA; a node in maintenance mode is reached through MaintenanceClient.
func NewMachineryTalosClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (TalosClient, error) {
	c, err := dialAdmin(ctx, cluster, m, bundle)
	if err != nil {
		return nil, err
	}

	return &machineryTalosClient{c: c, endpoint: m.DialEndpoint(), kubernetesCA: bundle.Certs.K8s.Crt}, nil
}

// dialAdmin dials m's Talos API with the bundle's os:admin identity, for every machinery-backed client here.
func dialAdmin(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (*client.Client, error) {
	cert, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, fmt.Errorf("generating admin client certificate: %w", err)
	}

	cfg := clientconfig.NewConfig(cluster.Name, []string{m.DialEndpoint()}, bundle.Certs.OS.Crt, cert)

	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dialing Talos API: %w", err)
	}

	return c, nil
}

// machineryTalosClient adapts a real *client.Client to TalosClient.
type machineryTalosClient struct {
	// endpoint and kubernetesCA let APIServerReady probe the node's kube-apiserver.
	endpoint     string
	kubernetesCA []byte

	c *client.Client
}

// Close releases the gRPC connection. It is not on the TalosClient interface: callers close through an
// io.Closer assertion (closeTalosClient), so the fake has nothing to implement.
func (t *machineryTalosClient) Close() error {
	return t.c.Close()
}

// CurrentConfig reads the MachineConfig COSI resource's active ID. Its Bytes() are the configuration exactly
// as last submitted (configloader keeps the original bytes rather than re-encoding), so it compares directly
// with pkg/render's hash.
func (t *machineryTalosClient) CurrentConfig(ctx context.Context) ([]byte, error) {
	md := resource.NewMetadata(configres.NamespaceName, configres.MachineConfigType, configres.ActiveID, resource.VersionUndefined)

	res, err := safe.StateGet[*configres.MachineConfig](ctx, t.c.COSI, md)
	if err != nil {
		return nil, fmt.Errorf("reading current machine configuration: %w", err)
	}

	return res.Provider().Bytes()
}

// Identity reads the node's hardware identity over the authenticated connection.
func (t *machineryTalosClient) Identity(ctx context.Context) (HardwareIdentity, error) {
	return ReadHardwareIdentity(ctx, t.c.COSI)
}

// NodeName reads the Nodename resource (k8s namespace, ID "nodename"): the name Talos registers
// this node under in Kubernetes, which is also what its etcd member is called.
func (t *machineryTalosClient) NodeName(ctx context.Context) (string, error) {
	res, err := safe.StateGetByID[*k8sres.Nodename](ctx, t.c.COSI, k8sres.NodenameID)
	if err != nil {
		return "", fmt.Errorf("reading the Kubernetes node name: %w", err)
	}

	return res.TypedSpec().Nodename, nil
}

// MachineStage reads the MachineStatus resource's stage ("running", "installing",
// "rebooting", ...).
func (t *machineryTalosClient) MachineStage(ctx context.Context) (string, error) {
	md := resource.NewMetadata(runtimeres.NamespaceName, runtimeres.MachineStatusType, runtimeres.MachineStatusID, resource.VersionUndefined)

	res, err := safe.StateGet[*runtimeres.MachineStatus](ctx, t.c.COSI, md)
	if err != nil {
		return "", fmt.Errorf("reading node status: %w", err)
	}

	return res.TypedSpec().Stage.String(), nil
}

// VolumeFailures lists the volumes whose VolumeStatus phase is failed with their error message, so a
// node stuck in stage booting says why (a TPM-sealed STATE volume on a machine without a TPM).
func (t *machineryTalosClient) VolumeFailures(ctx context.Context) (string, error) {
	list, err := safe.StateListAll[*blockres.VolumeStatus](ctx, t.c.COSI)
	if err != nil {
		return "", fmt.Errorf("listing volume statuses: %w", err)
	}

	var failed []string

	for vs := range list.All() {
		if spec := vs.TypedSpec(); spec.Phase == blockres.VolumePhaseFailed {
			failed = append(failed, vs.Metadata().ID()+": "+spec.ErrorMessage)
		}
	}

	slices.Sort(failed)

	return strings.Join(failed, "; "), nil
}

// Schematic reads the ImageFactorySchematic singleton (runtime namespace). Talos derives it from the
// "schematic" extension of the booted system, so after an upgrade it names the new installer's
// schematic. A node that did not boot an Image Factory asset has no such resource.
func (t *machineryTalosClient) Schematic(ctx context.Context) (string, error) {
	md := resource.NewMetadata(runtimeres.NamespaceName, runtimeres.ImageFactorySchematicType,
		runtimeres.ImageFactorySchematicID, resource.VersionUndefined)

	res, err := safe.StateGet[*runtimeres.ImageFactorySchematic](ctx, t.c.COSI, md)
	if err != nil {
		if state.IsNotFoundError(err) {
			return "", nil
		}

		return "", fmt.Errorf("reading the Image Factory schematic: %w", err)
	}

	return res.TypedSpec().SchematicID, nil
}

// SecurityState reads the node's boot security; see ReadSecurityState.
func (t *machineryTalosClient) SecurityState(ctx context.Context) (SecurityState, error) {
	return ReadSecurityState(ctx, t.c.COSI)
}

// DryRunApply validates cfg without applying it and collects every message's warnings.
func (t *machineryTalosClient) DryRunApply(ctx context.Context, cfg []byte) ([]string, error) {
	resp, err := t.c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_NO_REBOOT, DryRun: true,
	})
	if err != nil {
		return nil, err
	}

	var warnings []string

	for _, msg := range resp.GetMessages() {
		warnings = append(warnings, msg.GetWarnings()...)
	}

	return warnings, nil
}

// ApplyNoReboot applies cfg with ApplyConfigurationRequest_NO_REBOOT.
func (t *machineryTalosClient) ApplyNoReboot(ctx context.Context, cfg []byte) error {
	_, err := t.c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_NO_REBOOT,
	})

	return err
}

// ApplyStaged applies cfg with ApplyConfigurationRequest_STAGED. See TalosClient.ApplyStaged.
func (t *machineryTalosClient) ApplyStaged(ctx context.Context, cfg []byte) error {
	_, err := t.c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_STAGED,
	})

	return err
}

// Reboot calls this node's Reboot API with the default (graceful) mode.
func (t *machineryTalosClient) Reboot(ctx context.Context) error {
	return t.c.Reboot(ctx)
}

// EtcdMemberList lists etcd members as this node's own machined sees them (membership is raft-replicated,
// so any member reports the same list).
func (t *machineryTalosClient) EtcdMemberList(ctx context.Context) ([]EtcdMember, error) {
	resp, err := t.c.EtcdMemberList(ctx, &machineapi.EtcdMemberListRequest{QueryLocal: true})
	if err != nil {
		return nil, err
	}

	var members []EtcdMember

	for _, msg := range resp.GetMessages() {
		for _, m := range msg.GetMembers() {
			members = append(members, EtcdMember{ID: m.GetId(), Hostname: m.GetHostname()})
		}
	}

	return members, nil
}

// EtcdLeaveCluster asks this client's own node to leave etcd.
func (t *machineryTalosClient) EtcdLeaveCluster(ctx context.Context) error {
	return t.c.EtcdLeaveCluster(ctx, &machineapi.EtcdLeaveClusterRequest{})
}

// EtcdRemoveMemberByID forcibly removes memberID from etcd, as decided by
// this client's own node.
func (t *machineryTalosClient) EtcdRemoveMemberByID(ctx context.Context, memberID uint64) error {
	return t.c.EtcdRemoveMemberByID(ctx, &machineapi.EtcdRemoveMemberByIDRequest{MemberId: memberID})
}

// Reset wipes this node's STATE and EPHEMERAL partitions by label (never Mode: USER_DISKS, which the server
// rejects with a non-empty SystemPartitionsToWipe, nor the implicit wipe-everything of an empty one) and
// reboots it to maintenance mode. Graceful is false: the caller has drained and left etcd already. A wipeMode
// (reclaim.ResetRequestWipe) wipes the whole system disk instead.
func (t *machineryTalosClient) Reset(ctx context.Context, wipeMode string, powerOff bool) error {
	req := reclaim.ResetRequestWipe(false, wipeMode)
	req.Reboot = !powerOff // Talos powers the node off after the wipe when it is not told to reboot

	return t.c.ResetGeneric(ctx, req)
}

// Shutdown calls this node's Shutdown API. See TalosClient.Shutdown.
func (t *machineryTalosClient) Shutdown(ctx context.Context, force bool) error {
	return t.c.Shutdown(ctx, client.WithShutdownForce(force))
}

// Version reports this node's running Talos release tag.
func (t *machineryTalosClient) Version(ctx context.Context) (string, error) {
	resp, err := t.c.Version(ctx)
	if err != nil {
		return "", err
	}

	for _, msg := range resp.GetMessages() {
		if tag := msg.GetVersion().GetTag(); tag != "" {
			return tag, nil
		}
	}

	return "", fmt.Errorf("talos: version response carried no version tag")
}

// BootID reads bootIDPath over Talos's file-read API.
func (t *machineryTalosClient) BootID(ctx context.Context) (string, error) {
	reader, err := t.c.Read(ctx, bootIDPath)
	if err != nil {
		return "", err
	}
	defer reader.Close() //nolint:errcheck

	body, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(body)), nil
}

// Upgrade calls this node's Upgrade API. stage and force stay false: force would skip the server's own
// etcd-health validation of a control plane, which Ichor relies on instead of re-implementing.
//
// The legacy RPC is used, not LifecycleService: its streaming needs infrastructure this package lacks, and
// plain Upgrade works across the whole supported upgrade-from range (MinimumHostUpgradeVersion for 1.14 is
// 1.12.0, older than LifecycleService's >1.13.0-alpha.2).
//
//nolint:staticcheck // legacy MachineService.Upgrade, see above
func (t *machineryTalosClient) Upgrade(ctx context.Context, image string) error {
	_, err := t.c.Upgrade(ctx, image, false, false)

	return err
}

// PullImage pre-pulls image through the legacy ImagePull RPC, the one
// talosctl upgrade-k8s itself still uses; Unimplemented is not an error.
//
//nolint:staticcheck // Client.ImagePull is deprecated in favor of ImageServiceClient, which talosctl upgrade-k8s has not moved off either.
func (t *machineryTalosClient) PullImage(ctx context.Context, image string, system bool) error {
	namespace := common.ContainerdNamespace_NS_CRI
	if system {
		namespace = common.ContainerdNamespace_NS_SYSTEM
	}

	err := t.c.ImagePull(ctx, namespace, image)
	if status.Code(err) == codes.Unimplemented {
		return nil
	}

	return err
}

// Kubeconfig asks the dialed control plane for an admin kubeconfig.
func (t *machineryTalosClient) Kubeconfig(ctx context.Context) ([]byte, error) {
	return t.c.Kubeconfig(ctx)
}

// BootstrapManifests lists the k8s.Manifest resources of the control-plane namespace and flattens their
// items, as go-kubernetes' manifests.GetBootstrapManifests does for talosctl.
func (t *machineryTalosClient) BootstrapManifests(ctx context.Context) ([]map[string]any, error) {
	list, err := safe.StateListAll[*k8sres.Manifest](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing bootstrap manifests: %w", err)
	}

	var objects []map[string]any

	for manifest := range list.All() {
		for _, item := range manifest.TypedSpec().Items {
			objects = append(objects, item.Object)
		}
	}

	return objects, nil
}

// ServiceRestart restarts the Talos service id.
func (t *machineryTalosClient) ServiceRestart(ctx context.Context, id string) error {
	_, err := t.c.ServiceRestart(ctx, id)

	return err
}

// ServiceState reads the service from the Talos ServiceList API.
func (t *machineryTalosClient) ServiceState(ctx context.Context, id string) (string, bool, bool, error) {
	resp, err := t.c.ServiceList(ctx)
	if err != nil {
		return "", false, false, err
	}

	for _, msg := range resp.GetMessages() {
		for _, svc := range msg.GetServices() {
			if svc.GetId() != id {
				continue
			}

			h := svc.GetHealth()

			return svc.GetState(), h != nil && !h.GetUnknown(), h.GetHealthy(), nil
		}
	}

	return "", false, false, fmt.Errorf("service %q is not reported by the node", id)
}

// EtcdStatus reads this node's own etcd member status.
func (t *machineryTalosClient) EtcdStatus(ctx context.Context) (EtcdStatus, error) {
	resp, err := t.c.EtcdStatus(ctx)
	if err != nil {
		return EtcdStatus{}, err
	}

	for _, msg := range resp.GetMessages() {
		if ms := msg.GetMemberStatus(); ms != nil {
			return EtcdStatus{MemberID: ms.GetMemberId(), LeaderID: ms.GetLeader(), DBSize: ms.GetDbSize(), DBSizeInUse: ms.GetDbSizeInUse()}, nil
		}
	}

	return EtcdStatus{}, fmt.Errorf("talos: etcd status response carried no member status")
}

// EtcdAlarms lists the alarms etcd reports, named as etcd does.
func (t *machineryTalosClient) EtcdAlarms(ctx context.Context) ([]string, error) {
	resp, err := t.c.EtcdAlarmList(ctx)
	if err != nil {
		return nil, err
	}

	var alarms []string

	for _, msg := range resp.GetMessages() {
		for _, a := range msg.GetMemberAlarms() {
			if a.GetAlarm() != machineapi.EtcdMemberAlarm_NONE {
				alarms = append(alarms, a.GetAlarm().String())
			}
		}
	}

	return alarms, nil
}

// EtcdDefragment defragments this node's own member.
func (t *machineryTalosClient) EtcdDefragment(ctx context.Context) error {
	_, err := t.c.EtcdDefragment(ctx)

	return err
}

// EtcdForfeitLeadership asks this node's member to hand leadership over.
func (t *machineryTalosClient) EtcdForfeitLeadership(ctx context.Context) error {
	_, err := t.c.EtcdForfeitLeadership(ctx, &machineapi.EtcdForfeitLeadershipRequest{})

	return err
}

// EtcdDataPresent lists /var/lib/etcd/member, as Talos's own recovery test does to prove a wipe: a
// missing directory is the answer "no", whatever else fails is an error.
func (t *machineryTalosClient) EtcdDataPresent(ctx context.Context) (bool, error) {
	stream, err := t.c.MachineClient.List(ctx, &machineapi.ListRequest{Root: filepath.Join(constants.EtcdDataPath, "member")})
	if err != nil {
		return false, err
	}

	_, err = stream.Recv()

	switch {
	case err == nil, errors.Is(err, io.EOF):
		return true, nil
	case strings.Contains(client.Status(err).Message(), "no such file or directory"):
		return false, nil
	}

	return false, err
}

// ResetEtcdData is the wipe of Talos's disaster-recovery procedure (its integration test
// EtcdRecoverSuite): a non-graceful reset with reboot of EPHEMERAL plus every promotable system volume
// that is a dedicated partition (ETCD, CRI, KUBELET, LOG), because wiping EPHEMERAL only clears those
// that live as directories under it. STATE stays, so the node keeps its configuration and identity.
func (t *machineryTalosClient) ResetEtcdData(ctx context.Context) error {
	wipe := []*machineapi.ResetPartitionSpec{{Label: constants.EphemeralPartitionLabel, Wipe: true}}

	for _, id := range talosconfig.PromotableSystemVolumeNames {
		vs, err := safe.StateGetByID[*blockres.VolumeStatus](ctx, t.c.COSI, id)
		if err != nil || vs.TypedSpec().Type != blockres.VolumeTypePartition {
			continue // a directory under EPHEMERAL, or unknown: the EPHEMERAL wipe covers it
		}

		wipe = append(wipe, &machineapi.ResetPartitionSpec{Label: id, Wipe: true})
	}

	return t.c.ResetGeneric(ctx, &machineapi.ResetRequest{Graceful: false, Reboot: true, SystemPartitionsToWipe: wipe})
}

// EtcdRecover uploads the snapshot. See TalosClient.EtcdRecover.
func (t *machineryTalosClient) EtcdRecover(ctx context.Context, snapshot io.Reader) error {
	_, err := t.c.EtcdRecover(ctx, snapshot)

	return recoveryError(err)
}

// BootstrapRecover is `talosctl bootstrap --recover-from` after the upload.
func (t *machineryTalosClient) BootstrapRecover(ctx context.Context) error {
	return recoveryError(t.c.Bootstrap(ctx, &machineapi.BootstrapRequest{RecoverEtcd: true}))
}

// recoveryError maps the answers Talos's recovery calls give to the sentinels callers decide on:
// Unavailable (the node is rebooting or not listening yet), FailedPrecondition and DeadlineExceeded (not
// ready, time not in sync) mean wait, AlreadyExists means the etcd data directory is not empty.
func recoveryError(err error) error {
	switch client.StatusCode(err) {
	case codes.OK:
		return err
	case codes.Unavailable, codes.FailedPrecondition, codes.DeadlineExceeded:
		return fmt.Errorf("%w: %w", ErrRecoveryNotReady, err)
	case codes.AlreadyExists:
		return fmt.Errorf("%w: %w", ErrEtcdDataExists, err)
	}

	return err
}
