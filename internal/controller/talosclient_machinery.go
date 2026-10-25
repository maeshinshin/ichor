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

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// NewMachineryTalosClient dials m's Talos API using the bundle's os:admin
// identity and adapts the connection to TalosClient. It is what cmd/main.go
// wires into TalosMachineReconciler.TalosClient.
//
// The insecure, unauthenticated path Talos's maintenance mode offers is not
// an option this reconciler needs: design 5.6's configuration reconcile
// only ever runs against a machine that is already approved and already
// has a configuration, which means it has already joined and already
// trusts the bundle's CA. A node with no configuration yet is discovery's
// problem (phase 3), not this one's.
func NewMachineryTalosClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (TalosClient, error) {
	cert, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, fmt.Errorf("generating admin client certificate: %w", err)
	}

	cfg := clientconfig.NewConfig(cluster.Name, []string{m.Spec.Endpoint}, bundle.Certs.OS.Crt, cert)

	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dialing Talos API: %w", err)
	}

	return &machineryTalosClient{c: c}, nil
}

// machineryTalosClient adapts a real *client.Client to TalosClient.
type machineryTalosClient struct {
	c *client.Client
}

// Close releases the gRPC connection. It is not part of the TalosClient
// interface -- Reconcile closes it through an io.Closer type assertion
// instead, so the interface design 5.6 needs stays exactly three methods
// and the fake test double never has to implement a method it has nothing
// to close.
func (t *machineryTalosClient) Close() error {
	return t.c.Close()
}

// CurrentConfig reads the MachineConfig COSI resource's active ID. Its
// Bytes() returns the node's configuration exactly as last submitted --
// configloader parses ApplyConfiguration's payload into a readonly
// container that preserves the original bytes rather than re-encoding --
// so this is directly comparable to pkg/render's hash.
func (t *machineryTalosClient) CurrentConfig(ctx context.Context) ([]byte, error) {
	md := resource.NewMetadata(configres.NamespaceName, configres.MachineConfigType, configres.ActiveID, resource.VersionUndefined)

	res, err := safe.StateGet[*configres.MachineConfig](ctx, t.c.COSI, md)
	if err != nil {
		return nil, fmt.Errorf("reading current machine configuration: %w", err)
	}

	return res.Provider().Bytes()
}

// DryRunApply validates cfg without applying it and collects every
// message's warnings (ADR 0009: ValidateAtRuntime's warnings, not
// Mode/ModeDetails, are what's worth reading from this call).
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

// EtcdMemberList lists etcd members as this node's own machined sees them
// (etcd's membership is raft-replicated, so any member -- not only a
// control plane the request happens to be "about" -- reports the same
// list).
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

// Reset wipes this node's STATE and EPHEMERAL partitions by label (never
// Mode: USER_DISKS, which the server rejects alongside a non-empty
// SystemPartitionsToWipe, and never the implicit "wipe everything" that
// an empty SystemPartitionsToWipe would mean) and reboots it back to
// maintenance mode. Graceful: false -- the caller has already drained and
// left etcd itself (ADR 0032) by the time this is ever called, so Talos's
// own graceful pre-reset routine would find nothing left to do.
func (t *machineryTalosClient) Reset(ctx context.Context) error {
	return t.c.ResetGeneric(ctx, &machineapi.ResetRequest{
		Graceful: false,
		Reboot:   true,
		SystemPartitionsToWipe: []*machineapi.ResetPartitionSpec{
			{Label: constants.StatePartitionLabel, Wipe: true},
			{Label: constants.EphemeralPartitionLabel, Wipe: true},
		},
	})
}
