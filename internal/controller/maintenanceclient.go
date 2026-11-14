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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// MaintenanceClient is the maintenance-mode Talos API surface discovery and the first apply need,
// over the unauthenticated connection maintenance mode offers (docs/design/architecture.md "Nodes":
// "reads hardware identity, disks and NICs over an insecure connection").
type MaintenanceClient interface {
	// Identity reads the node's SMBIOS UUID, serial number and permanent MAC
	// addresses -- the identity tuple architecture.md's "Nodes" section
	// defines -- plus the manufacturer, shown to an operator resolving a weak
	// or colliding identity but never itself part of the tuple.
	Identity(ctx context.Context) (HardwareIdentity, error)

	// ApplyConfiguration hands cfg to a node that is still in maintenance mode
	// (the insecure path; the same boundary discovery reads identity over). A nil error means only that the node stored
	// the
	// configuration: its install, reboot and join happen afterwards, which is
	// why the reconciler follows the node's stage instead of trusting this
	// return. A node that already has a configuration refuses it.
	ApplyConfiguration(ctx context.Context, cfg []byte) error

	// Inventory reads the node's installable disks and its physical NICs with permanent addresses,
	// the lists approval picks from. Discovery records them on the TalosMachine's status.
	Inventory(ctx context.Context) (Inventory, error)

	// SecurityState reads the node's SecureBoot state over the maintenance API (`talosctl get
	// securitystate --insecure`), so a cluster that requires SecureBoot can refuse a machine that
	// booted without it before giving it a configuration.
	SecurityState(ctx context.Context) (SecurityState, error)

	// TalosVersion reads the node's running Talos release tag ("vX.Y.Z") over the maintenance API
	// (`talosctl version --insecure`), so init can refuse a node older than the release it renders for.
	TalosVersion(ctx context.Context) (string, error)
}

// Inventory is what MaintenanceClient.Inventory reads from a node's maintenance API.
type Inventory struct {
	Disks []ichorv1alpha1.TalosMachineDiscoveredDisk
	Links []ichorv1alpha1.TalosMachineDiscoveredLink
}

// HardwareIdentity is what MaintenanceClient.Identity reads from a node's
// maintenance API.
type HardwareIdentity struct {
	UUID         string
	Serial       string
	MACAddresses []string
	Manufacturer string
}
