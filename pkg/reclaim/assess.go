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

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Known is what Ichor already manages. A node whose identity or cluster ID is
// in it is in state (iii) of ADR 0103 and is never reclaimed.
type Known struct {
	// MachineUUIDs are the SMBIOS UUIDs of every approved TalosMachine.
	MachineUUIDs []string
	// ClusterIDs are the Talos cluster IDs of every Ichor cluster.
	ClusterIDs []string
}

// Options are the operator's flags.
type Options struct {
	// Force allows a control plane whose removal breaks the foreign etcd quorum
	// (or the last control plane of a reachable cluster). It also needs the
	// second typed confirmation, Confirmation.ClusterName.
	Force bool
	// ForceUngraceful sends a non-graceful reset, for a foreign cluster that is
	// dead. Without it a graceful reset is used, and an unreachable cluster is a
	// refusal.
	ForceUngraceful bool
}

// Plan is the verdict on an Inspection.
type Plan struct {
	// Graceful is the Graceful field the reset will carry.
	Graceful bool
	// Refusals are why the reset must not be sent. Empty means it may be, once
	// confirmed.
	Refusals []string
	// NeedsForce is true when Options.Force was needed and given: the operator
	// must also type the foreign cluster's name.
	NeedsForce bool
	// Warnings are shown beside the inspection.
	Warnings []string
}

// Refused returns the refusals as one error, or nil.
func (p Plan) Refused() error {
	if len(p.Refusals) == 0 {
		return nil
	}

	return fmt.Errorf("refusing to reclaim: %s", strings.Join(p.Refusals, "; "))
}

// Assess applies the refusals of ADR 0103 section 2.1 to in. It is pure.
func Assess(in Inspection, known Known, opt Options) Plan {
	var p Plan

	switch {
	case in.UUID == "":
		p.Refusals = append(p.Refusals, "the node reports no UUID, so there is nothing to confirm it by")
	case containsFold(known.MachineUUIDs, in.UUID):
		p.Refusals = append(p.Refusals, fmt.Sprintf("node %s is already an Ichor machine", in.UUID))
	}

	if in.ClusterID != "" && containsFold(known.ClusterIDs, in.ClusterID) {
		p.Refusals = append(p.Refusals, fmt.Sprintf(
			"node belongs to cluster %q, which Ichor already manages; a reset is not needed, add it to that Ichor cluster with "+
				"`ichor adopt --cluster NAME --add-machine %s`", in.ClusterName, in.Address))
	}

	p.Graceful = in.ClusterReachable && !opt.ForceUngraceful

	if !in.ClusterReachable && !opt.ForceUngraceful {
		p.Refusals = append(p.Refusals, fmt.Sprintf(
			"cluster %q is not reachable from the node, so a graceful reset (cordon, drain, leave etcd) would hang; "+
				"if that cluster is dead, pass --force-ungraceful", in.ClusterName))
	}

	if in.ControlPlane && in.EtcdKnown {
		if reason := quorumBreak(in); reason != "" {
			if opt.Force {
				p.NeedsForce = true

				p.Warnings = append(p.Warnings, reason+" (--force given)")
			} else {
				p.Refusals = append(p.Refusals, reason+"; pass --force to override")
			}
		}
	}

	return p
}

// quorumBreak says why removing this control plane breaks its cluster's etcd,
// or "". etcd needs a majority: of n members, n-1 left must still be at least
// n/2+1, which holds from three members up. One member is the last control
// plane of the cluster.
func quorumBreak(in Inspection) string {
	n := len(in.EtcdMembers)

	switch {
	case n <= 1:
		return fmt.Sprintf("this is the last control plane of cluster %q", in.ClusterName)
	case n == 2:
		return fmt.Sprintf("cluster %q has 2 etcd members; removing one loses quorum", in.ClusterName)
	}

	// Health: the members left after the removal must still hold a majority
	// of themselves that answer. The node being removed does not count.
	remaining, healthy := n-1, n-1

	for _, m := range in.EtcdMembers {
		if m.Unhealthy && (in.LocalMemberID == 0 || m.ID != in.LocalMemberID) {
			healthy--
		}
	}

	if healthy < remaining/2+1 {
		return fmt.Sprintf("cluster %q has %d etcd members of which %d would be unhealthy after the removal; "+
			"the remaining %d need %d healthy for quorum", in.ClusterName, n, remaining-healthy, remaining, remaining/2+1)
	}

	return ""
}

// Confirmation is what the operator typed. A --yes flag never fills it.
type Confirmation struct {
	// UUID must equal the node's UUID.
	UUID string
	// ClusterName must equal the foreign cluster's name; it is only checked
	// when the plan NeedsForce.
	ClusterName string
}

// Check returns nil only when c matches in (UUID case-insensitively, as SMBIOS
// UUIDs are written both ways) and, for a forced plan, the cluster name too.
func (c Confirmation) Check(in Inspection, p Plan) error {
	if in.UUID == "" || !strings.EqualFold(strings.TrimSpace(c.UUID), in.UUID) {
		return errors.New("the typed UUID does not match the node's UUID; nothing was sent")
	}

	if p.NeedsForce && strings.TrimSpace(c.ClusterName) != in.ClusterName {
		return errors.New("the typed cluster name does not match the node's cluster; nothing was sent")
	}

	return nil
}

// Execute is the one place a reset is sent. It re-inspects the node (so a
// stale talosconfig or a swapped node is caught after the operator looked),
// refuses if the fresh inspection is refused, checks the confirmation against
// the fresh UUID, and only then sends ResetRequest. It returns the inspection
// it acted on.
func Execute(ctx context.Context, n Node, known Known, opt Options, c Confirmation) (Inspection, error) {
	in, err := n.Inspect(ctx)
	if err != nil {
		return in, fmt.Errorf("inspecting the node before the reset: %w", err)
	}

	plan := Assess(in, known, opt)
	if err := plan.Refused(); err != nil {
		return in, err
	}

	if err := c.Check(in, plan); err != nil {
		return in, err
	}

	if err := n.Reset(ctx, plan.Graceful); err != nil {
		return in, fmt.Errorf("reset request to %s failed: %w", in.Address, err)
	}

	return in, nil
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(v string) bool { return strings.EqualFold(v, s) })
}
