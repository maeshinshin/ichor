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

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Node actions (docs/adr/0085): each starts one TalosOperation of kind node, or, for remove, deletes
// the machine through the removal flow. The CLI only writes those CRs; the controller does the work.

var nodeActionOpts struct {
	Follow      bool
	Confirm     string
	AllowLeader bool
	Node        string
	WipeMode    string
}

// nodeActionSpec is what one node action command asks for.
type nodeActionSpec struct {
	Machine     string
	Action      pb.NodeAction
	Service     string
	AllowLeader bool
	// Confirm is the machine name, typed or passed with --confirm; required for reset and remove.
	Confirm string
	Follow  bool
	// WipeMode is --wipe-mode of reset: system-disk or all; empty wipes STATE and EPHEMERAL only.
	WipeMode string
}

func (s nodeActionSpec) request(bg *pb.Override) *pb.NodeActionRequest {
	return &pb.NodeActionRequest{
		Machine: objectRef(apiOpts.Namespace, s.Machine), Action: s.Action, Service: s.Service,
		AllowLeader: s.AllowLeader, Confirm: s.Confirm, Override: bg, WipeMode: s.WipeMode,
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
	case pb.NodeAction_NODE_ACTION_RESET:
		return "RESET and remove"
	case pb.NodeAction_NODE_ACTION_REMOVE:
		return "REMOVE"
	case pb.NodeAction_NODE_ACTION_UNSPECIFIED:
	}

	return "Act on"
}

// needsTypedName is true for the actions that cannot be undone and so make the user type the name.
func needsTypedName(a pb.NodeAction) bool {
	return a == pb.NodeAction_NODE_ACTION_RESET || a == pb.NodeAction_NODE_ACTION_REMOVE
}

// runNodeAction plans, confirms and starts one node action. --yes skips the y/N question; reset
// and remove still need the node's name, typed here or given with --confirm.
func runNodeAction(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, s nodeActionSpec) error {
	bg := overrideOf(apiOpts)

	plan, err := c.Operation.PlanOperation(ctx, connect.NewRequest(&pb.PlanOperationRequest{
		Kind: pb.OperationKind_OPERATION_KIND_NODE, Node: s.request(bg),
	}))
	if err != nil {
		return err
	}

	name := strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s.Action.String(), "NODE_ACTION_")), "_", "-")

	if b := plan.Msg.GetBlocked(); b != "" {
		return fmt.Errorf("node %s: %s", name, b)
	}

	if s.Action == pb.NodeAction_NODE_ACTION_RESET && bg == nil {
		return errors.New("node reset: it needs --override (and the override RBAC verb on the machine)")
	}

	text := nodePlanText(s, plan.Msg)

	if !assumeYes && needsTypedName(s.Action) && s.Confirm == "" {
		_, _ = fmt.Fprint(errw, text)

		typed, err := readLine(in, errw, fmt.Sprintf("Type the node name %q to confirm: ", s.Machine))
		if err != nil {
			return fmt.Errorf("node %s: reading confirmation: %w", name, err)
		}

		if typed != s.Machine {
			return fmt.Errorf("node %s: aborted (the name did not match)", name)
		}

		s.Confirm = typed
	} else if err := confirmPlan(in, errw, "node "+name, text); err != nil {
		return err
	}

	if needsTypedName(s.Action) && s.Confirm != s.Machine {
		return fmt.Errorf("node %s: type the node name with --confirm %s to go on without asking", name, s.Machine)
	}

	if s.Action == pb.NodeAction_NODE_ACTION_REMOVE {
		resp, err := c.Machine.RemoveMachine(ctx, connect.NewRequest(&pb.RemoveMachineRequest{
			Machine: objectRef(apiOpts.Namespace, s.Machine), Confirm: s.Confirm,
		}))
		if err != nil {
			return err
		}

		if err := printAPI(out, resp.Msg, renderMachineRemoved(s.Machine)); err != nil {
			return err
		}

		nodeRemoveNext(errw, s.Machine)

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

// readLine prompts and returns the trimmed answer.
func readLine(r io.Reader, w io.Writer, prompt string) (string, error) {
	_, _ = fmt.Fprint(w, prompt)

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}

	return strings.TrimSpace(line), nil
}

func newNodeActionCmd(use, short string, action pb.NodeAction, args cobra.PositionalArgs, spec func(args []string) nodeActionSpec) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: short + `.

It shows the plan and asks for confirmation unless --yes. The CLI only writes the operation
(or deletes the machine); the controller does the work. Follow it with --follow.`,
		Args: args,
		RunE: func(cmd *cobra.Command, a []string) error {
			s := spec(a)
			s.Action, s.Follow, s.Confirm = action, nodeActionOpts.Follow, nodeActionOpts.Confirm
			s.WipeMode = nodeActionOpts.WipeMode

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
	nodeRebootCmd = newNodeActionCmd("reboot NODE", "Reboot a node gracefully: cordon, drain, reboot, wait until it is Ready again",
		pb.NodeAction_NODE_ACTION_REBOOT, cobra.ExactArgs(1), machineArg)
	nodeShutdownCmd = newNodeActionCmd("shutdown NODE", "Shut a node down; it stays off until you power it on",
		pb.NodeAction_NODE_ACTION_SHUTDOWN, cobra.ExactArgs(1), machineArg)
	nodeResetCmd = newNodeActionCmd("reset NODE",
		"Remove a node from the cluster and wipe it (its STATE and EPHEMERAL partitions, or the whole disk with --wipe-mode); needs the override permission",
		pb.NodeAction_NODE_ACTION_RESET, cobra.ExactArgs(1), machineArg)
	nodeRemoveCmd = newNodeActionCmd("remove NODE", "Remove a node from the cluster: drain it and, for a control plane, remove its etcd member",
		pb.NodeAction_NODE_ACTION_REMOVE, cobra.ExactArgs(1), machineArg)
	nodeRestartServiceCmd = newNodeActionCmd("restart-service NODE SERVICE",
		"Restart a Talos service on a node (kubelet, containerd or cri)",
		pb.NodeAction_NODE_ACTION_RESTART_SERVICE, cobra.ExactArgs(2), func(a []string) nodeActionSpec {
			return nodeActionSpec{Machine: a[0], Service: a[1]}
		})

	etcdDefragCmd = newNodeActionCmd("defrag", "Defragment the etcd member of a control plane node",
		pb.NodeAction_NODE_ACTION_ETCD_DEFRAG, cobra.NoArgs, func([]string) nodeActionSpec {
			return nodeActionSpec{Machine: nodeActionOpts.Node, AllowLeader: nodeActionOpts.AllowLeader}
		})
	etcdForfeitCmd = newNodeActionCmd("forfeit-leadership", "Hand etcd leadership over from a control plane node that leads",
		pb.NodeAction_NODE_ACTION_ETCD_FORFEIT_LEADERSHIP, cobra.NoArgs, func([]string) nodeActionSpec {
			return nodeActionSpec{Machine: nodeActionOpts.Node}
		})
)

func init() {
	nodeCmd.AddCommand(nodeRebootCmd, nodeShutdownCmd, nodeResetCmd, nodeRemoveCmd, nodeRestartServiceCmd)
	etcdCmd.AddCommand(etcdDefragCmd, etcdForfeitCmd)

	for _, c := range []*cobra.Command{etcdDefragCmd, etcdForfeitCmd} {
		c.Flags().StringVar(&nodeActionOpts.Node, "node", "", "the control plane node (required)")
		_ = c.MarkFlagRequired("node")
	}

	etcdDefragCmd.Flags().BoolVar(&nodeActionOpts.AllowLeader, "allow-leader", false,
		"allow defragmenting the etcd leader (writes pause for the whole cluster meanwhile)")

	nodeResetCmd.Flags().StringVar(&nodeActionOpts.WipeMode, "wipe-mode", "",
		"wipe more than STATE and EPHEMERAL: system-disk (the whole system disk, so the node boots its boot image again and can be installed afresh) or all (also every user disk)")

	for _, c := range []*cobra.Command{nodeResetCmd, nodeRemoveCmd} {
		c.Flags().StringVar(&nodeActionOpts.Confirm, "confirm", "", "the node's name, to go on without being asked to type it")
	}
}
