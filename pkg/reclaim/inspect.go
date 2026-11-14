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

package reclaim

import "context"

// Volume is something on a node that a reset does not wipe but that the
// operator should know about: a user volume or a raw volume on a disk other
// than the system disk.
type Volume struct {
	// Name is the volume's ID or label, or its device path when it has neither.
	Name string
	// Location is the device path.
	Location string
	// Size is in bytes.
	Size uint64
	// Encrypted is true for a LUKS volume. Only a key locked to STATE (lockToState) needs the
	// encryption salt the reset deletes, so only its data is unreadable afterwards.
	Encrypted bool
}

// EtcdMember is one member of the node's etcd cluster.
type EtcdMember struct {
	ID       uint64
	Hostname string
	// Unhealthy is true when the member failed its health probe. The zero value
	// (healthy or not probed) never causes a refusal.
	Unhealthy bool
}

// Inspection is what a read-only look at the node found. It carries no
// credential material.
type Inspection struct {
	// Address is the node's Talos API address as dialled.
	Address string
	// UUID is the SMBIOS UUID, the identity the operator types to confirm.
	UUID string
	// Hostname is the node's hostname.
	Hostname string
	// ControlPlane is true for a control plane (or init) node.
	ControlPlane bool
	// ClusterName and ClusterID identify the foreign cluster the node belongs to.
	ClusterName string
	ClusterID   string
	// Version is the running Talos release tag.
	Version string
	// ClusterReachable is true when the foreign cluster answers from this node's
	// point of view: for a control plane its etcd member list could be read; for
	// a worker its cluster endpoint accepts a TCP connection. A graceful reset
	// needs it.
	ClusterReachable bool
	// EtcdMembers is the foreign cluster's etcd membership, for a control plane
	// whose list could be read (EtcdKnown).
	EtcdMembers []EtcdMember
	EtcdKnown   bool
	// EtcdProblem is why a control plane's etcd membership could not be read: etcd is not
	// running, or did not answer within the call timeout. It is empty for a worker and for a
	// control plane whose list was read. It is the "foreign cluster does not exist" case of
	// No quorum can be judged and no graceful leave is possible.
	EtcdProblem string
	// LocalMemberID is the etcd member ID of the dialled node itself.
	LocalMemberID uint64
	// Volumes are the orphaned volumes: not wiped by the reset, possibly
	// encrypted.
	Volumes []Volume
}

// Node is one Talos node reached with the operator's credential. Dial returns
// the real implementation; tests and the controller can supply their own.
type Node interface {
	// Inspect reads, and writes nothing.
	Inspect(ctx context.Context) (Inspection, error)
	// Reset sends the reset request. It returns once Talos accepted it.
	Reset(ctx context.Context, graceful bool) error
	// Close releases the connection.
	Close() error
}
