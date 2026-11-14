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
	"slices"
	"strings"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// NewMachineryMaintenanceClient dials endpoint's Talos API in maintenance mode (unauthenticated, all a
// node with no configuration offers) and adapts the connection to MaintenanceClient. cmd/main.go wires
// it into DiscoveryReconciler.MaintenanceClient.
//
// The connection is held to the MaintenancePin of ctx (WithMaintenancePin), or to the certificate of
// its own first handshake: a node that answers with another certificate halfway is refused.
func NewMachineryMaintenanceClient(ctx context.Context, endpoint string) (MaintenanceClient, error) {
	pin := PinFromContext(ctx)

	c, err := client.New(ctx, pin.ClientOptions(endpoint)...)
	if err != nil {
		return nil, fmt.Errorf("dialing maintenance Talos API: %w", err)
	}

	return &machineryMaintenanceClient{c: c, pin: pin}, nil
}

// machineryMaintenanceClient adapts a real *client.Client, connected in
// maintenance mode, to MaintenanceClient.
type machineryMaintenanceClient struct {
	c   *client.Client
	pin *MaintenancePin
}

// PeerFingerprint is the SPKI fingerprint of the certificate the node presented.
func (m *machineryMaintenanceClient) PeerFingerprint() string { return m.pin.Seen() }

// Close releases the gRPC connection (the io.Closer convention talosclient_machinery.go uses).
func (m *machineryMaintenanceClient) Close() error { return m.c.Close() }

// ApplyConfiguration sends cfg over the insecure connection with mode AUTO, the mode `talosctl
// apply-config --insecure` uses: in maintenance mode the node installs and reboots as the
// configuration requires. It returns once the node has stored the configuration, not once the
// install is done.
func (m *machineryMaintenanceClient) ApplyConfiguration(ctx context.Context, cfg []byte) error {
	_, err := m.c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_AUTO,
	})

	return err
}

// configuredNodeRejection reports whether err is a node that already has a configuration refusing
// an unauthenticated call: its API completed the TLS handshake and then the server
// rejected the missing client certificate (a TLS alert about the certificate, which gRPC reports as
// Unavailable) or answered Unauthenticated/PermissionDenied. A handshake that never completes (nothing
// listening, not TLS) is deliberately not matched.
func configuredNodeRejection(err error) bool {
	if err == nil {
		return false
	}

	switch status.Code(err) { //nolint:exhaustive
	case codes.Unauthenticated, codes.PermissionDenied:
		return true
	case codes.Unavailable:
		msg := err.Error()

		return strings.Contains(msg, "remote error: tls:") && strings.Contains(msg, "certificate")
	default:
		return false
	}
}

// Identity reads the node's hardware identity over the unauthenticated maintenance API; see
// ReadHardwareIdentity. A node that already has a configuration rejects the call and the error
// wraps ErrAlreadyConfigured.
func (m *machineryMaintenanceClient) Identity(ctx context.Context) (HardwareIdentity, error) {
	id, err := ReadHardwareIdentity(ctx, m.c.COSI)
	if configuredNodeRejection(err) {
		return id, fmt.Errorf("%w: %w", ErrAlreadyConfigured, err)
	}

	return id, err
}

// ReadHardwareIdentity reads hardware.SystemInformation for the SMBIOS UUID, serial number and
// manufacturer, and network.LinkStatus for the permanent MAC of every physical link. A missing
// SystemInformation (not every platform reports SMBIOS data) is not an error: UUID and serial stay empty,
// which strongIdentity treats as a weak key. Errors wrap the underlying gRPC error so callers can classify them.
func ReadHardwareIdentity(ctx context.Context, cosi state.State) (HardwareIdentity, error) {
	var id HardwareIdentity

	sysInfo, err := safe.StateGet[*hardware.SystemInformation](ctx, cosi, resource.NewMetadata(
		hardware.NamespaceName, hardware.SystemInformationType, hardware.SystemInformationID, resource.VersionUndefined,
	))

	switch {
	case err == nil:
		spec := sysInfo.TypedSpec()
		id.UUID, id.Serial, id.Manufacturer = spec.UUID, spec.SerialNumber, spec.Manufacturer
	case state.IsNotFoundError(err):
		// No SMBIOS data on this platform; id stays empty, a weak key.
	default:
		return id, fmt.Errorf("reading system information: %w", err)
	}

	links, err := safe.StateListAll[*network.LinkStatus](ctx, cosi)
	if err != nil {
		return id, fmt.Errorf("listing link status: %w", err)
	}

	for i := range links.Len() {
		spec := links.Get(i).TypedSpec()
		if !spec.Physical() || len(spec.PermanentAddr) == 0 {
			continue
		}

		id.MACAddresses = append(id.MACAddresses, spec.PermanentAddr.String())
	}

	return id, nil
}

// Inventory reads the node's disks and NICs over the unauthenticated maintenance API; see ReadInventory.
func (m *machineryMaintenanceClient) Inventory(ctx context.Context) (Inventory, error) {
	return ReadInventory(ctx, m.c.COSI)
}

// SecurityState reads the node's boot security over the unauthenticated maintenance API; see ReadSecurityState.
func (m *machineryMaintenanceClient) SecurityState(ctx context.Context) (SecurityState, error) {
	return ReadSecurityState(ctx, m.c.COSI)
}

// TalosVersion reads the node's Talos release tag over the unauthenticated maintenance API (Version is served
// to a node with no configuration, which is what `talosctl version --insecure` relies on).
func (m *machineryMaintenanceClient) TalosVersion(ctx context.Context) (string, error) {
	resp, err := m.c.Version(ctx)
	if err != nil {
		return "", err
	}

	for _, msg := range resp.GetMessages() {
		if tag := msg.GetVersion().GetTag(); tag != "" {
			return tag, nil
		}
	}

	return "", errors.New("the node reported no Talos version")
}

// ReadInventory lists the disks an install or a disk role can take (block.Disk, minus the CD-ROM drives
// and read-only disks the approval pickers leave out) and the physical links with a permanent address
// (network.LinkStatus), the same links ReadHardwareIdentity takes MACs from. Both lists come
// back sorted, so an unchanged node writes an unchanged status.
func ReadInventory(ctx context.Context, cosi state.State) (Inventory, error) {
	var inv Inventory

	disks, err := safe.StateListAll[*block.Disk](ctx, cosi)
	if err != nil {
		return inv, fmt.Errorf("listing disks: %w", err)
	}

	for i := range disks.Len() {
		spec := disks.Get(i).TypedSpec()
		if spec.CDROM || spec.Readonly {
			continue
		}

		inv.Disks = append(inv.Disks, discoveredDisk(spec))
	}

	links, err := safe.StateListAll[*network.LinkStatus](ctx, cosi)
	if err != nil {
		return inv, fmt.Errorf("listing link status: %w", err)
	}

	for i := range links.Len() {
		spec := links.Get(i).TypedSpec()
		if !spec.Physical() || len(spec.PermanentAddr) == 0 {
			continue
		}

		inv.Links = append(inv.Links, ichorv1alpha1.TalosMachineDiscoveredLink{
			Name:         links.Get(i).Metadata().ID(),
			PermanentMAC: spec.PermanentAddr.String(),
			LinkUp:       spec.LinkState,
			SpeedMbit:    int32(spec.SpeedMegabits), //nolint:gosec // link speeds fit in int32
			Driver:       spec.Driver,
		})
	}

	slices.SortFunc(inv.Disks, func(a, b ichorv1alpha1.TalosMachineDiscoveredDisk) int {
		return strings.Compare(a.DevPath, b.DevPath)
	})
	slices.SortFunc(inv.Links, func(a, b ichorv1alpha1.TalosMachineDiscoveredLink) int {
		return strings.Compare(a.Name, b.Name)
	})

	return inv, nil
}

// discoveredDisk maps one block.Disk to the status record, taking the by-id path from its symlinks
// (block.Disk has no field of its own for it).
func discoveredDisk(spec *block.DiskSpec) ichorv1alpha1.TalosMachineDiscoveredDisk {
	var byID string

	for _, s := range spec.Symlinks {
		if strings.Contains(s, "/by-id/") {
			byID = s

			break
		}
	}

	return ichorv1alpha1.TalosMachineDiscoveredDisk{
		WWID: spec.WWID, Serial: spec.Serial, ByIDPath: byID, BusPath: spec.BusPath, DevPath: spec.DevPath,
		SizeBytes: int64(spec.Size), //nolint:gosec // disk sizes fit in int64
		Model:     spec.Model, Transport: spec.Transport,
	}
}
