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
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// opWatchInterval is how often --watch/--follow polls an operation. A var so
// tests can shrink it. The API has no stream for operations: the status is a
// handful of fields and a poll costs one Get.
var opWatchInterval = 2 * time.Second

var operationOpts struct {
	Watch bool
}

var operationCmd = &cobra.Command{
	Use:     "operation",
	Aliases: []string{"op"},
	Short:   "List, follow, pause, resume or delete the operations (upgrades, shutdowns) of a cluster",
	Long: `An operation is the record of one machine-by-machine action: a Talos,
Kubernetes or Ichor upgrade, a kube-proxy switch, or a cluster shutdown. These
commands read and change the records; ` + "`ichor upgrade`, `ichor cni` and `ichor cluster shutdown`" + ` start them.`,
}

var operationListCmd = &cobra.Command{
	Use:   useList,
	Short: "List operations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return showAPIHuman(cmd, false, func(ctx context.Context, c *apiClients) (*connect.Response[pb.ListOperationsResponse], error) {
			return c.Operation.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{
				Cluster: clusterFilter(apiOpts.Namespace, operationClusterFlag),
			}))
		}, renderOperationList)
	},
}

var operationClusterFlag string

var operationGetCmd = &cobra.Command{
	Use:   useGetName,
	Short: "Show one operation: phase, failure reason and the state of each machine",
	Long: `get prints the operation's status as the controller wrote it. With --watch it
keeps polling, prints a progress line to stderr whenever the status changes, and
exits when the operation completes (or pauses itself because a machine failed,
which is an error exit).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			ref := objectRef(apiOpts.Namespace, args[0])
			if operationOpts.Watch {
				return watchOperation(ctx, c, ref, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}

			resp, err := c.Operation.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{Operation: ref}))
			if err != nil {
				return err
			}

			return printAPI(cmd.OutOrStdout(), resp.Msg, renderOperationGet)
		})
	},
}

func newPauseCmd(use, short string, paused bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " NAME",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
				return runSetPaused(ctx, c, cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], paused)
			})
		},
	}
}

func runSetPaused(ctx context.Context, c *apiClients, out, errw io.Writer, name string, paused bool) error {
	resp, err := c.Operation.SetOperationPaused(ctx, connect.NewRequest(&pb.SetOperationPausedRequest{
		Operation: objectRef(apiOpts.Namespace, name), Paused: paused,
	}))
	if err != nil {
		return err
	}

	// Resuming an operation that failed also asks for its failed step to run again (ADR 0095).
	if op := resp.Msg.GetOperation(); !paused && op.GetPhase() == condPaused && op.GetFailureReason() != "" {
		rerunNotice(errw, op.GetFailureReason())
	}

	return printAPI(out, resp.Msg, renderOperationPaused)
}

var operationPauseCmd = newPauseCmd("pause", "Stop the operation from advancing any further machine (the cluster lock stays held, nothing is undone)", true)
var operationResumeCmd = newPauseCmd("resume", "Let a paused operation continue; one paused by a failed step runs that step again, so fix the cause first", false)

var operationDeleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "Delete an operation record",
	Long: `delete removes the record and, when it has not finished, lets the controller release
the cluster lock. It does not stop a step already sent to a machine and it does not
roll anything back. It asks for confirmation unless --yes.

A ca-rotation or key-rotation deleted before it made the new CA or key issue is abandoned: the
new material leaves the bundle, the lock is released and the machines are rendered from the old
material again (the nodes then report a pending change; 'ichor config apply' drops what they
still accept). Later in the rotation the delete is refused: let it finish.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			return runOperationDelete(ctx, c, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
		})
	},
}

func runOperationDelete(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, name string) error {
	ref := objectRef(apiOpts.Namespace, name)

	if !assumeYes {
		got, err := c.Operation.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{Operation: ref}))
		if err != nil {
			return err
		}

		op := got.Msg.GetOperation()

		if err := confirmOrAbort(in, errw, "operation delete", fmt.Sprintf(
			"Delete operation %s/%s (%s on cluster %s, phase %q)? Steps already sent to machines are not undone. [y/N] ",
			ref.GetNamespace(), name, kindName(op.GetKind()), op.GetCluster().GetName(), op.GetPhase()), "aborted"); err != nil {
			return err
		}
	}

	resp, err := c.Operation.DeleteOperation(ctx, connect.NewRequest(&pb.DeleteOperationRequest{Operation: ref}))
	if err != nil {
		return err
	}

	return printAPI(out, resp.Msg, func(h *human, _ *pb.DeleteOperationResponse) error {
		h.Note(h.Mark(markOK) + " deleted operation " + ref.GetNamespace() + "/" + name)

		return h.Err()
	})
}

func kindName(k pb.OperationKind) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(k.String(), "OPERATION_KIND_")), "_", "-")
}

// caStages is the order a CA rotation goes through its stages (ADR 0091).
var caStages = []string{
	ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials,
	ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping,
	ichorv1alpha1.CARotationStageFinalizing, ichorv1alpha1.CARotationStageDone,
}

// passedStages are the stages between last and stage, which a poll did not see: a stage shorter than
// the poll interval is still on its way. From the start (last "") every stage before stage is passed.
func passedStages(last, stage string) []string {
	to := slices.Index(caStages, stage)
	from := slices.Index(caStages, last)

	if to < 0 || from >= to {
		return nil
	}

	return caStages[from+1 : to]
}

// stageProgress prints a progress line whenever it changes, and before the line of a stage it saw, a
// line for each stage a poll jumped over, so the whole sequence is in the output.
type stageProgress struct {
	w     io.Writer
	last  string // the last line printed
	stage string // the last stage seen
}

// show prints the lines for stage; line renders the progress line for a given stage.
func (p *stageProgress) show(stage string, line func(stage string) string) {
	for _, s := range passedStages(p.stage, stage) {
		p.print(line(s))
	}

	p.print(line(stage))
	p.stage = stage
}

func (p *stageProgress) print(line string) {
	if line != p.last {
		_, _ = fmt.Fprintln(p.w, line)
		p.last = line
	}
}

// progressLine is one line of --watch output.
func progressLine(op *pb.Operation) string {
	return progressLineAt(op, cmp.Or(op.GetCaRotationStage(), op.GetKeyRotationStage()))
}

// progressLineAt is progressLine with the CA rotation stage given, so that a stage a poll skipped can be
// printed in its place.
func progressLineAt(op *pb.Operation, caStage string) string {
	var done, running int

	parts := make([]string, 0, len(op.GetMachines()))

	for _, m := range op.GetMachines() {
		switch m.GetPhase() {
		case "Completed":
			done++
		case "InProgress":
			running++
		}

		parts = append(parts, m.GetName()+"="+m.GetPhase())
	}

	line := fmt.Sprintf("%s %s: phase %s", kindName(op.GetKind()), op.GetRef().GetName(), cmp.Or(op.GetPhase(), "-"))
	if len(parts) > 0 {
		line += fmt.Sprintf(", %d done, %d in progress", done, running)
	}

	if caStage != "" {
		line += ", stage " + caStage
	}

	if st := op.GetEtcdRestoreStep(); st != "" {
		line += ", step " + st
	}

	if len(parts) > 0 {
		line += " [" + strings.Join(parts, " ") + "]"
	}

	if op.GetFailureReason() != "" {
		line += ": " + op.GetFailureReason()
	} else if op.GetPhase() == "InProgress" && op.GetMessage() != "" {
		line += ": " + op.GetMessage()
	}

	return line
}

// watchOperation polls the operation, prints a progress line whenever it
// changes, and ends with the operation's final message on out. A pause caused
// by a failed machine is an error exit (the controller never retries it); a
// pause by the operator is waited out.
func watchOperation(ctx context.Context, c *apiClients, ref *pb.ObjectRef, out, errw io.Writer) error {
	return watchOperationUntil(ctx, c, ref, out, errw, 0)
}

// restoreResumeWindow bounds how long a live restore's --follow waits for the operation to come back:
// the restore rolls the API back to a snapshot taken before the operation existed, so right after the
// API answers again the operation is NotFound until the controller recreates it from the marker.
var restoreResumeWindow = 15 * time.Minute

// watchRestoreOperation is watchOperation for a live etcd restore: the operation being NotFound is
// expected for a while, and is waited out (ADR 0089).
func watchRestoreOperation(ctx context.Context, c *apiClients, ref *pb.ObjectRef, out, errw io.Writer) error {
	return watchOperationUntil(ctx, c, ref, out, errw, restoreResumeWindow)
}

// watchOperationUntil is the watch; goneWindow > 0 treats NotFound as transient for that long.
func watchOperationUntil(ctx context.Context, c *apiClients, ref *pb.ObjectRef, out, errw io.Writer, goneWindow time.Duration) error {
	var (
		rep       = newOpReporter(errw)
		goneSince time.Time
	)

	for {
		var resp *connect.Response[pb.GetOperationResponse]

		err := retryTransient(ctx, c, rep.retryWriter(), func() (err error) {
			resp, err = c.Operation.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{Operation: ref}))

			return err
		})
		switch {
		case goneWindow > 0 && connect.CodeOf(err) == connect.CodeNotFound:
			if goneSince.IsZero() {
				goneSince = time.Now()

				rep.gone()
			}

			if time.Since(goneSince) > goneWindow {
				return rep.abort(err, false)
			}
		case err != nil:
			return rep.abort(err, ctx.Err() != nil)
		default:
			goneSince = time.Time{}

			op := resp.Msg.GetOperation()

			rep.show(op)

			failed := opFailed(op)
			if opFinished(op) || failed {
				rep.end(op)

				if err := printAPI(out, resp.Msg, renderOperationGet); err != nil {
					return err
				}

				if failed {
					return fmt.Errorf("operation %s paused after a failure: %s (fix the cause, then `ichor operation resume %[1]s`)", ref.GetName(), op.GetFailureReason())
				}

				return nil
			}
		}

		if err := sleepCtx(ctx, opWatchInterval); err != nil {
			return rep.abort(err, true)
		}
	}
}

// startSpec is what an upgrade, kube-proxy or shutdown command asks for.
type startSpec struct {
	Kind           pb.OperationKind
	Cluster        string // name; empty means the only cluster in the namespace
	Target         string
	MaxUnavailable int32
	Follow         bool
	// Override is the ADR 0110 override the plan and the create carry; nil when off.
	Override *pb.Override
	// Installer is the installer image settings of an Ichor upgrade (ADR 0122); nil for the other kinds.
	Installer *pb.IchorInstallerOptions
	// CATargets are the CAs of a ca-rotation (ADR 0120); empty is the default pair.
	CATargets []string
	// KeyRotation is the request of a key-rotation start (ADR 0121); nil for every other kind.
	KeyRotation *pb.KeyRotationRequest

	// AfterCreate, when set, takes over once the operation exists: it gets the operation's ref and
	// the create response and does the output and any follow itself (ca-rotation's credential
	// hand-off, which cannot wait behind a tunnel the rotation breaks).
	AfterCreate func(ctx context.Context, ref *pb.ObjectRef, created *pb.CreateOperationResponse) error
}

// resolveCluster returns the cluster the command acts on: --cluster, or the
// only cluster in the namespace.
func resolveCluster(ctx context.Context, c *apiClients, name string) (*pb.ObjectRef, error) {
	if name != "" {
		return objectRef(apiOpts.Namespace, name), nil
	}

	resp, err := c.Cluster.ListClusters(ctx, connect.NewRequest(&pb.ListClustersRequest{}))
	if err != nil {
		return nil, err
	}

	var names []string

	for _, cl := range resp.Msg.GetClusters() {
		if cl.GetRef().GetNamespace() == apiOpts.Namespace {
			names = append(names, cl.GetRef().GetName())
		}
	}

	if len(names) != 1 {
		return nil, fmt.Errorf("name the cluster with --cluster (namespace %q has %d: %s)", apiOpts.Namespace, len(names), strings.Join(names, ", "))
	}

	return objectRef(apiOpts.Namespace, names[0]), nil
}

// confirmPlan shows the plan on errw and asks to proceed. --yes skips only the question; the plan
// is always shown. what prefixes the errors.
func confirmPlan(in io.Reader, errw io.Writer, what, text string) error {
	if assumeYes {
		_, _ = fmt.Fprint(errw, text)

		return nil
	}

	return confirmOrAbort(in, errw, what, text+"Proceed? [y/N] ", "aborted")
}

// confirmOrAbort asks prompt and returns nil only on a yes. what prefixes the errors; aborted is
// the error text for a no.
func confirmOrAbort(in io.Reader, errw io.Writer, what, prompt, aborted string) error {
	ok, err := confirm(in, errw, prompt)
	if err != nil {
		return fmt.Errorf("%s: reading confirmation: %w", what, err)
	}

	if !ok {
		return fmt.Errorf("%s: %s", what, aborted)
	}

	return nil
}

// runStartOperation is the shared body of `upgrade`, `cni kube-proxy` and
// `cluster shutdown`: ask the server for the plan, show it, confirm (unless --yes), create.
// The server changes the spec first and creates the operation second, and
// refuses everything it can before the first write (docs/adr/0068).
func runStartOperation(ctx context.Context, c *apiClients, in io.Reader, out, errw io.Writer, s startSpec) error {
	cluster, err := resolveCluster(ctx, c, s.Cluster)
	if err != nil {
		return err
	}

	plan, err := c.Operation.PlanOperation(ctx, connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: cluster, Kind: s.Kind, Target: s.Target, MaxUnavailable: s.MaxUnavailable, Override: s.Override, IchorInstaller: s.Installer, CaTargets: s.CATargets,
		KeyRotation: s.KeyRotation,
	}))
	if err != nil {
		return err
	}

	if b := plan.Msg.GetBlocked(); b != "" {
		return fmt.Errorf("%s: %s", kindName(s.Kind), b)
	}

	if err := confirmPlan(in, errw, kindName(s.Kind), planText(s.Kind, cluster.GetName(), plan.Msg)); err != nil {
		return err
	}

	resp, err := c.Operation.CreateOperation(ctx, connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: cluster, Kind: s.Kind, Target: s.Target, MaxUnavailable: s.MaxUnavailable, Override: s.Override, IchorInstaller: s.Installer, CaTargets: s.CATargets,
		KeyRotation: s.KeyRotation,
	}))
	if err != nil {
		return err
	}

	if s.AfterCreate != nil {
		return s.AfterCreate(ctx, resp.Msg.GetOperation().GetRef(), resp.Msg)
	}

	if !s.Follow {
		return printAPI(out, resp.Msg, renderOperationCreated)
	}

	announceCreated(errw, resp.Msg.GetOperation().GetRef().GetName())

	return watchOperation(ctx, c, resp.Msg.GetOperation().GetRef(), out, errw)
}

func init() {
	rootCmd.AddCommand(operationCmd)
	addAPIFlags(operationCmd)
	operationCmd.AddCommand(operationListCmd, operationGetCmd, operationPauseCmd, operationResumeCmd, operationDeleteCmd)
	operationListCmd.Flags().StringVar(&operationClusterFlag, "cluster", "", "only the operations of this cluster")
	operationGetCmd.Flags().BoolVarP(&operationOpts.Watch, "watch", "w", false, "keep polling and print progress until the operation finishes")
	markWrites(operationPauseCmd, operationResumeCmd, operationDeleteCmd)
}
