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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Restore into a live cluster (docs/adr/0089): `ichor restore --live` writes one TalosOperation of kind
// etcd-restore; the controller wipes etcd on every control plane and recovers the snapshot. Unlike
// --recover-cluster it needs the cluster, ichor-ui or the kubeconfig, and the controller.

var restoreLiveOpts struct {
	Live    bool
	Backup  string
	Confirm string
	Follow  bool
}

// liveRestoreSpec is what `ichor restore --live` was asked for.
type liveRestoreSpec struct {
	Cluster string
	// Backup and Snapshot may be empty: the cluster's only backup, and its newest snapshot.
	Backup   string
	Snapshot string
	// Confirm is the cluster name, typed or passed with --confirm.
	Confirm string
	Follow  bool
}

// chooseSnapshot picks the backup and snapshot to restore from the cluster's backups.
func chooseSnapshot(backups []*pb.EtcdBackup, s liveRestoreSpec) (*pb.EtcdBackup, *pb.EtcdSnapshot, error) {
	withSnapshots := slices.DeleteFunc(slices.Clone(backups), func(b *pb.EtcdBackup) bool {
		return s.Backup != "" && b.GetRef().GetName() != s.Backup
	})

	if len(withSnapshots) == 0 {
		if s.Backup != "" {
			return nil, nil, fmt.Errorf("restore: cluster %q has no TalosEtcdBackup named %q", s.Cluster, s.Backup)
		}

		return nil, nil, fmt.Errorf("restore: cluster %q has no TalosEtcdBackup, so there is nothing to restore from", s.Cluster)
	}

	if len(withSnapshots) > 1 {
		names := make([]string, len(withSnapshots))
		for i, b := range withSnapshots {
			names[i] = b.GetRef().GetName()
		}

		return nil, nil, fmt.Errorf("restore: cluster %q has several backups (%s): choose one with --backup", s.Cluster, strings.Join(names, ", "))
	}

	b := withSnapshots[0]

	if s.Snapshot == "" {
		if len(b.GetSnapshots()) == 0 {
			return nil, nil, fmt.Errorf("restore: backup %q lists no snapshot yet", b.GetRef().GetName())
		}

		return b, b.GetSnapshots()[0], nil // newest first
	}

	for _, snap := range b.GetSnapshots() {
		if snap.GetName() == s.Snapshot {
			return b, snap, nil
		}
	}

	return nil, nil, fmt.Errorf("restore: backup %q does not list snapshot %q; it lists %s", b.GetRef().GetName(), s.Snapshot, snapshotNames(b))
}

func snapshotNames(b *pb.EtcdBackup) string {
	names := make([]string, 0, len(b.GetSnapshots()))

	for _, snap := range b.GetSnapshots() {
		names = append(names, snap.GetName())
	}

	if len(names) == 0 {
		return "none"
	}

	return strings.Join(names, ", ")
}

// liveRestoreText is the confirmation of a live restore, from the server's plan.
func liveRestoreText(s liveRestoreSpec, snap *pb.EtcdSnapshot, p *pb.PlanOperationResponse) string {
	var b strings.Builder

	when := snap.GetTime().AsTime().UTC().Format(time.RFC3339)
	fmt.Fprintf(&b, "RESTORE cluster %q from snapshot %s (taken %s).\n", s.Cluster, snap.GetName(), when)

	for i, step := range p.GetSteps() {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
	}

	if w := p.GetWarning(); w != "" {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}

	b.WriteString("If a step fails the operation pauses; nothing is rolled back. This cannot be undone.\n")

	return b.String()
}

// runLiveRestore lists the backups, plans, confirms with the typed cluster name and an override
// reason, and starts the operation. --yes skips the y/N question only: the name is still needed.
func runLiveRestore(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, s liveRestoreSpec) error {
	cluster, err := resolveCluster(ctx, c, s.Cluster)
	if err != nil {
		return err
	}

	s.Cluster = cluster.GetName()

	if s.Follow {
		enableFailover(ctx, c, errw, cluster) // read now, while the control planes still answer
	}

	bg := overrideOf(apiOpts)
	if bg == nil {
		return errors.New("restore --live: it needs --override <reason> (and the override RBAC verb on the cluster); the reason goes into the audit record")
	}

	list, err := c.Operation.ListEtcdBackups(ctx, connect.NewRequest(&pb.ListEtcdBackupsRequest{Cluster: cluster}))
	if err != nil {
		return err
	}

	backup, snap, err := chooseSnapshot(list.Msg.GetBackups(), s)
	if err != nil {
		return err
	}

	req := &pb.EtcdRestoreRequest{Backup: backup.GetRef(), Snapshot: snap.GetName(), Override: bg}

	plan, err := c.Operation.PlanOperation(ctx, connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: cluster, Kind: pb.OperationKind_OPERATION_KIND_ETCD_RESTORE, EtcdRestore: req,
	}))
	if err != nil {
		return err
	}

	if blocked := plan.Msg.GetBlocked(); blocked != "" {
		return fmt.Errorf("restore --live: %s", blocked)
	}

	text := liveRestoreText(s, snap, plan.Msg)

	switch {
	case s.Confirm != "":
		_, _ = fmt.Fprint(errw, text)
	case assumeYes:
		return fmt.Errorf("restore --live: type the cluster name with --confirm %s to go on without asking", s.Cluster)
	default:
		_, _ = fmt.Fprint(errw, text)

		typed, err := readLine(in, errw, fmt.Sprintf("Type the cluster name %q to confirm: ", s.Cluster))
		if err != nil {
			return fmt.Errorf("restore --live: reading confirmation: %w", err)
		}

		s.Confirm = typed
	}

	if s.Confirm != s.Cluster {
		return errors.New("restore --live: aborted (the cluster name did not match)")
	}

	req.Confirm = s.Confirm

	resp, err := c.Operation.CreateOperation(ctx, connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: cluster, Kind: pb.OperationKind_OPERATION_KIND_ETCD_RESTORE, EtcdRestore: req,
	}))
	if err != nil {
		return err
	}

	if !s.Follow {
		return writeMessage(out, outputFormat, resp.Msg)
	}

	_, _ = fmt.Fprintf(errw, "created operation %s\n", resp.Msg.GetOperation().GetRef().GetName())

	return watchRestoreOperation(ctx, c, resp.Msg.GetOperation().GetRef(), out, errw)
}
