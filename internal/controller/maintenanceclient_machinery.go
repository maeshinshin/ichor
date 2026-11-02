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
	"strings"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewMachineryMaintenanceClient dials endpoint's Talos API in maintenance
// mode -- unauthenticated, which is all a node with no configuration yet can
// offer (docs/design/architecture.md "Nodes") -- and adapts the connection
// to MaintenanceClient. It is what cmd/main.go wires into
// DiscoveryReconciler.MaintenanceClient.
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

// Close releases the gRPC connection, through the same io.Closer convention
// talosclient_machinery.go uses.
func (m *machineryMaintenanceClient) Close() error {
	return m.c.Close()
}

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

// ReadHardwareIdentity reads hardware.SystemInformation for the SMBIOS UUID, serial
// number and manufacturer, and network.LinkStatus for the permanent MAC
// address of every physical link. A missing SystemInformation resource (not
// every platform reports SMBIOS data) is not an error here: it leaves the
// identity's UUID and serial empty, which DiscoveryReconciler's
// strongIdentity treats as a weak key, exactly as architecture.md's "Nodes"
// requires. Errors wrap the underlying gRPC error so callers can classify them.
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
		// No SMBIOS data on this platform (e.g. virtio-blk-style virtualized
		// nodes); id stays empty, which is correctly a weak key.
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
