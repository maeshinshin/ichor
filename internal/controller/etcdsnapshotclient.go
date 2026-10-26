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
	"io"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// EtcdNodeStatus is what the backup controller needs to know about one
// control plane's etcd member to choose where to snapshot.
type EtcdNodeStatus struct {
	MemberID  uint64
	LeaderID  uint64
	IsLearner bool
	// Errors are etcd's own alarm strings for this member; non-empty means unhealthy.
	Errors []string
}

// EtcdSnapshotClient is the Talos API surface TalosEtcdBackup needs from one
// control-plane node. A separate interface from TalosClient on purpose: it
// keeps every existing TalosClient fake untouched.
type EtcdSnapshotClient interface {
	EtcdStatus(ctx context.Context) (EtcdNodeStatus, error)
	// EtcdSnapshot streams a consistent snapshot of the node's local etcd
	// member: the bbolt file followed by its sha256 (see etcdbackup.Verifier).
	EtcdSnapshot(ctx context.Context) (io.ReadCloser, error)
	Close() error
}

// EtcdSnapshotClientFactory dials m, like TalosClientFactory-style wiring in
// cmd/main.go. Tests inject a fake.
type EtcdSnapshotClientFactory func(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (EtcdSnapshotClient, error)

// NewMachineryEtcdSnapshotClient is the real EtcdSnapshotClientFactory.
func NewMachineryEtcdSnapshotClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (EtcdSnapshotClient, error) {
	c, err := dialAdmin(ctx, cluster, m, bundle)
	if err != nil {
		return nil, err
	}

	return &machineryEtcdSnapshotClient{c: c}, nil
}

type machineryEtcdSnapshotClient struct{ c *client.Client }

func (t *machineryEtcdSnapshotClient) Close() error { return t.c.Close() }

func (t *machineryEtcdSnapshotClient) EtcdStatus(ctx context.Context) (EtcdNodeStatus, error) {
	resp, err := t.c.EtcdStatus(ctx)
	if err != nil {
		return EtcdNodeStatus{}, err
	}

	for _, msg := range resp.GetMessages() {
		if ms := msg.GetMemberStatus(); ms != nil {
			return EtcdNodeStatus{
				MemberID: ms.GetMemberId(), LeaderID: ms.GetLeader(), IsLearner: ms.GetIsLearner(), Errors: ms.GetErrors(),
			}, nil
		}
	}

	return EtcdNodeStatus{}, errors.New("talos: etcd status response carried no member status")
}

func (t *machineryEtcdSnapshotClient) EtcdSnapshot(ctx context.Context) (io.ReadCloser, error) {
	return t.c.EtcdSnapshot(ctx, &machine.EtcdSnapshotRequest{})
}
