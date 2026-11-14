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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Node actions: each starts one TalosOperation of kind node, or, for remove, deletes
// the machine through the removal flow. The CLI only writes those CRs; the controller does the work.

var nodeActionOpts struct {
	Follow      bool
	Confirm     string
	AllowLeader bool
	Node        string
	Reboot      bool
	WipeMode    string
}

// nodeActionSpec is what one node action command asks for.
type nodeActionSpec struct {
	Machine     string
	Action      pb.NodeAction
	Service     string
	AllowLeader bool
	// Confirm is the machine name, typed or passed with --confirm; required for remove.
	Confirm string
	Follow  bool
	// Reboot is --reboot of remove: reboot into maintenance mode after the wipe instead of powering off.
	Reboot bool
	// WipeMode is --wipe-mode of remove: "" (system-partitions), system-disk or all; see wipeModeWire.
	WipeMode string
}

// wipeModeSystemPartitions is the --wipe-mode name of the default wipe, STATE and EPHEMERAL only.
const wipeModeSystemPartitions = "system-partitions"

// wipeModeWire maps the --wipe-mode flag to the API's wipe mode, where the default is empty.
func wipeModeWire(flag string) (string, error) {
	switch flag {
	case "", wipeModeSystemPartitions:
		return "", nil
	case ichorv1alpha1.WipeModeSystemDisk, ichorv1alpha1.WipeModeAll, ichorv1alpha1.WipeModeSystemDiskAndUserDisks:
		return flag, nil
	}

	return "", fmt.Errorf("--wipe-mode %q is not one of %s, %s, %s, %s", flag, wipeModeSystemPartitions, ichorv1alpha1.WipeModeSystemDisk, ichorv1alpha1.WipeModeAll, ichorv1alpha1.WipeModeSystemDiskAndUserDisks)
}

func (s nodeActionSpec) request(bg *pb.Override) *pb.NodeActionRequest {
	return &pb.NodeActionRequest{
		Machine: objectRef(apiOpts.Namespace, s.Machine), Action: s.Action, Service: s.Service,
		AllowLeader: s.AllowLeader, Confirm: s.Confirm, Override: bg, Reboot: s.Reboot, WipeMode: s.WipeMode,
	}
}

// nodePlanText is the confirmation of a node action, from the server's plan.
func nodePlanText(s nodeActionSpec, p *pb.PlanOperationResponse) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s node %q.\n", nodeHeadline(s), s.Machine)

	for i, step := range p.GetSteps() {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, step)
	}

	if w := p.GetWarning(); w != "" {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}

	if s.Action != pb.NodeAction_NODE_ACTION_REMOVE {
		b.WriteString("If a step fails the operation pauses; nothing is rolled back.\n")
	}

	return b.String()
}

func nodeHeadline(s nodeActionSpec) string {
	switch s.Action {
	case pb.NodeAction_NODE_ACTION_REBOOT:
		return "Reboot"
	case pb.NodeAction_NODE_ACTION_SHUTDOWN:
		return "SHUT DOWN"
	case pb.NodeAction_NODE_ACTION_RESTART_SERVICE:
		return "Restart the " + s.Service + " service on"
	case pb.NodeAction_NODE_ACTION_ETCD_DEFRAG:
		return "Defragment etcd on"
	case pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP:
		return "Hand etcd leadership over from"
	case pb.NodeAction_NODE_ACTION_REMOVE:
		return "REMOVE and wipe"
	case pb.NodeAction_NODE_ACTION_UNSPECIFIED:
	}

	return "Act on"
}

// needsTypedName is true for the actions that cannot be undone and so make the user type the name.
func needsTypedName(a pb.NodeAction) bool {
	return a == pb.NodeAction_NODE_ACTION_REMOVE
}

// nodeActionCommand is the command that starts a node action, for the messages that name it.
func nodeActionCommand(a pb.NodeAction) string {
	switch a {
	case pb.NodeAction_NODE_ACTION_REBOOT:
		return "reboot node"
	case pb.NodeAction_NODE_ACTION_SHUTDOWN:
		return "shutdown node"
	case pb.NodeAction_NODE_ACTION_REMOVE:
		return "delete node"
	case pb.NodeAction_NODE_ACTION_RESTART_SERVICE:
		return "restart service"
	case pb.NodeAction_NODE_ACTION_ETCD_DEFRAG:
		return "defrag etcd"
	case pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP:
		return "forfeit-leadership etcd"
	case pb.NodeAction_NODE_ACTION_UNSPECIFIED:
	}

	return "node action"
}

// runNodeAction plans, confirms and starts one node action. --yes skips the y/N question; remove
// needs the machine name instead, typed here or given with --confirm, which replaces the question.
func runNodeAction(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, s nodeActionSpec) error {
	bg := overrideOf(apiOpts)

	wipe, err := wipeModeWire(s.WipeMode)
	if err != nil {
		return err
	}

	s.WipeMode = wipe

	plan, err := c.Operation.PlanOperation(ctx, connect.NewRequest(&pb.PlanOperationRequest{
		Kind: pb.OperationKind_OPERATION_KIND_NODE, Node: s.request(bg),
	}))
	if err != nil {
		return err
	}

	name := nodeActionCommand(s.Action)

	if b := plan.Msg.GetBlocked(); b != "" {
		return fmt.Errorf("%s: %s", name, b)
	}

	if needsTypedName(s.Action) && bg == nil {
		return fmt.Errorf("%s: it wipes the node, so it needs --override (and the override RBAC verb on the node)", name)
	}

	// The typed name is the TalosMachine's name, as the CRD rule and RemoveMachine check it; NODE
	// may be its hostname or Node name, so the plan says which machine that is.
	machine := s.Machine
	if n := plan.Msg.GetConfirmName(); n != "" {
		machine = n
	}

	text := nodePlanText(s, plan.Msg)

	// The typed name is the consent for remove: typed at the prompt or given as --confirm
	// (as restore etcd and reclaim node do), it replaces the y/N question; --yes alone does not.
	switch {
	case needsTypedName(s.Action) && s.Confirm != "":
		_, _ = fmt.Fprint(errw, text)
	case !assumeYes && needsTypedName(s.Action):
		_, _ = fmt.Fprint(errw, text)

		typed, err := readLine(in, errw, confirmPrompt(machine, s.Machine))
		if err != nil {
			return fmt.Errorf("%s: reading confirmation: %w", name, err)
		}

		if typed != machine {
			return fmt.Errorf("%s: aborted (the name did not match; type %s)", name, confirmWhat(machine, s.Machine))
		}

		s.Confirm = typed
	default:
		if err := confirmPlan(in, errw, name, text); err != nil {
			return err
		}
	}

	if needsTypedName(s.Action) && s.Confirm != machine {
		return fmt.Errorf("%s: type %s with --confirm to go on without asking", name, confirmWhat(machine, s.Machine))
	}

	if s.Action == pb.NodeAction_NODE_ACTION_REMOVE {
		resp, err := c.Machine.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{
			Machine: objectRef(apiOpts.Namespace, s.Machine), Confirm: s.Confirm, Reboot: s.Reboot, Override: bg, WipeMode: s.WipeMode,
		}))
		if err != nil {
			return err
		}

		if err := printAPI(out, resp.Msg, renderMachineRemoved(s.Machine)); err != nil {
			return err
		}

		nodeRemoveNext(errw, s.Reboot, s.WipeMode)

		return nil
	}

	resp, err := c.Operation.CreateOperation(ctx, connect.NewRequest(&pb.CreateOperationRequest{
		Kind: pb.OperationKind_OPERATION_KIND_NODE, Node: s.request(bg),
	}))
	if err != nil {
		return err
	}

	if !s.Follow {
		return printAPI(out, resp.Msg, renderOperationCreated)
	}

	announceCreated(errw, resp.Msg.GetOperation().GetRef().GetName())

	return watchOperation(ctx, c, resp.Msg.GetOperation().GetRef(), out, errw)
}

// confirmWhat names what to type: the machine name, and the node as the user gave it when that differs.
func confirmWhat(machine, asked string) string {
	if asked == machine {
		return "the node name " + machine
	}

	return fmt.Sprintf("the node name %s (node %s)", machine, asked)
}

func confirmPrompt(machine, asked string) string {
	return "Type " + confirmWhat(machine, asked) + " to confirm: "
}

// readLine prompts and returns the trimmed answer.
func readLine(r io.Reader, w io.Writer, prompt string) (string, error) {
	_, _ = fmt.Fprint(w, prompt)

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

// nodeRemoveLong is the part of delete node's help that the other node actions do not share.
const nodeRemoveLong = `delete node --wipe-mode chooses what the wipe erases: system-partitions (the default) wipes the STATE and
EPHEMERAL partitions, so the node boots its installed system again from a fresh state; system-disk
wipes the whole system disk, boot and EFI included, so the node starts from its boot image (ISO or PXE)
and installs afresh, which is how a node is moved onto SecureBoot images (docs/guides/hardening.md);
all erases the same whole system disk as system-disk and never a user disk. system-disk-and-user-disks
erases that system disk and every user disk, except CD-ROM and read-only disks.
system-disk, all and system-disk-and-user-disks are irreversible.`

func newNodeActionCmd(use, short string, action pb.NodeAction, args cobra.PositionalArgs, spec func(args []string) nodeActionSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: short + `.

It shows the plan and asks for confirmation unless --yes. For delete node the typed node name
(the TalosMachine name, or --confirm NAME) is the confirmation and replaces the question; --yes alone does not. The CLI only writes the operation
(or deletes the node); the controller does the work. Follow it with --follow.`,
		Args: args,
		RunE: func(cmd *cobra.Command, a []string) error {
			s := spec(a)
			s.Action, s.Follow, s.Confirm, s.Reboot, s.WipeMode = action, nodeActionOpts.Follow, nodeActionOpts.Confirm, nodeActionOpts.Reboot, nodeActionOpts.WipeMode

			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
				return runNodeAction(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), s)
			})
		},
	}
	cmd.Flags().BoolVar(&nodeActionOpts.Follow, "follow", false, "after creating the operation, follow its progress until it finishes")
	markWrites(cmd)

	return cmd
}

func machineArg(a []string) nodeActionSpec { return nodeActionSpec{Machine: a[0]} }

var (
	rebootNodeCmd = newNodeActionCmd("node NAME", "Reboot a node gracefully: cordon, drain, reboot, wait until it is Ready again",
		pb.NodeAction_NODE_ACTION_REBOOT, cobra.ExactArgs(1), machineArg)
	shutdownNodeCmd = newNodeActionCmd("node NAME", "Shut a node down; it stays off until you power it on",
		pb.NodeAction_NODE_ACTION_SHUTDOWN, cobra.ExactArgs(1), machineArg)
	deleteNodeCmd = newNodeActionCmd("node NAME", "Delete a node from the cluster: drain it, wipe it (STATE and EPHEMERAL, or more with --wipe-mode), power it off (or reboot it with --reboot) and delete its Kubernetes Node; needs the override permission",
		pb.NodeAction_NODE_ACTION_REMOVE, cobra.ExactArgs(1), machineArg)
	restartServiceCmd = newNodeActionCmd("service NODE SERVICE",
		"Restart a Talos service on a node (kubelet, containerd or cri)",
		pb.NodeAction_NODE_ACTION_RESTART_SERVICE, cobra.ExactArgs(2), func(a []string) nodeActionSpec {
			return nodeActionSpec{Machine: a[0], Service: a[1]}
		})

	defragEtcdCmd = newNodeActionCmd("etcd --node CONTROL_PLANE", "Defragment the etcd member of a control plane node",
		pb.NodeAction_NODE_ACTION_ETCD_DEFRAG, cobra.NoArgs, func([]string) nodeActionSpec {
			return nodeActionSpec{Machine: nodeActionOpts.Node, AllowLeader: nodeActionOpts.AllowLeader}
		})
	forfeitLeadershipEtcdCmd = newNodeActionCmd("etcd --node CONTROL_PLANE", "Hand etcd leadership over from a control plane node that leads",
		pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP, cobra.NoArgs, func([]string) nodeActionSpec {
			return nodeActionSpec{Machine: nodeActionOpts.Node}
		})
)

func init() {
	rebootCmd.AddCommand(rebootNodeCmd)
	shutdownCmd.AddCommand(shutdownNodeCmd)
	deleteCmd.AddCommand(deleteNodeCmd)
	restartCmd.AddCommand(restartServiceCmd)
	defragCmd.AddCommand(defragEtcdCmd)
	forfeitLeadershipCmd.AddCommand(forfeitLeadershipEtcdCmd)

	rebootNodeCmd.Example = `  ichor reboot node worker-3
  ichor reboot node worker-3 --follow`
	shutdownNodeCmd.Example = `  ichor shutdown node worker-3`
	deleteNodeCmd.Example = `  ichor delete node worker-3
  ichor delete node worker-3 --confirm worker-3 --wipe-mode system-disk --override`
	restartServiceCmd.Example = `  ichor restart service worker-3 kubelet`
	defragEtcdCmd.Example = `  ichor defrag etcd --node cp-2
  ichor defrag etcd --node cp-1 --allow-leader`
	forfeitLeadershipEtcdCmd.Example = `  ichor forfeit-leadership etcd --node cp-1`

	for _, c := range []*cobra.Command{rebootNodeCmd, shutdownNodeCmd, deleteNodeCmd, restartServiceCmd, defragEtcdCmd, forfeitLeadershipEtcdCmd} {
		addAPIFlags(c)
	}

	for _, c := range []*cobra.Command{defragEtcdCmd, forfeitLeadershipEtcdCmd} {
		c.Flags().StringVar(&nodeActionOpts.Node, "node", "", "the control plane node (required)")
		_ = c.MarkFlagRequired("node")
	}

	defragEtcdCmd.Flags().BoolVar(&nodeActionOpts.AllowLeader, "allow-leader", false,
		"allow defragmenting the etcd leader (writes pause for the whole cluster meanwhile)")

	deleteNodeCmd.Flags().BoolVar(&nodeActionOpts.Reboot, "reboot", false,
		"after the wipe reboot the node into maintenance mode, so it can be added again, instead of powering it off")
	deleteNodeCmd.Long += "\n\n" + nodeRemoveLong
	deleteNodeCmd.Flags().StringVar(&nodeActionOpts.WipeMode, "wipe-mode", wipeModeSystemPartitions,
		"what to wipe: system-partitions (STATE and EPHEMERAL), system-disk (the whole system disk) all (the whole system disk, as system-disk; user disks are not wiped) or system-disk-and-user-disks (the whole system disk and every user disk)")
	deleteNodeCmd.Flags().StringVar(&nodeActionOpts.Confirm, "confirm", "", "the node name (the TalosMachine name, not the hostname), to go on without being asked to type it")
}
