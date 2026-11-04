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
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Human output of the operation commands (docs/adr/0118): list, get, the result of a start, pause,
// resume and delete, and the live view of --follow and get --watch.

const (
	opColName      = "name"
	opColMachine   = "machine"
	opColAge       = "age"
	opLabelCreated = "Created"
	// opGoneMessage is said once when a live etcd restore's operation vanishes with the rolled-back API.
	opGoneMessage  = "the operation is gone from the API (the cluster was rolled back to the snapshot); waiting for the restore to resume…"
	opLabelPhase   = "Phase"
	opLabelKind    = "Kind"
	opLabelCluster = "Cluster"
	opPhaseWaiting = ichorv1alpha1.TalosOperationPhasePending
)

// opFailed is an operation that paused itself because a step failed (an operator's pause has no reason).
func opFailed(op *pb.Operation) bool {
	return op.GetPhase() == condPaused && op.GetFailureReason() != ""
}

// opFinished is an operation that has nothing left to do.
func opFinished(op *pb.Operation) bool {
	return op.GetPhase() == ichorv1alpha1.TalosOperationPhaseCompleted || op.GetPhase() == ichorv1alpha1.TalosOperationPhaseShutDown
}

// opMark is the glyph of an operation's phase.
func opMark(op *pb.Operation) mark {
	switch {
	case opFinished(op):
		return markOK
	case opFailed(op):
		return markFailed
	case op.GetPhase() == condPaused:
		return markWarning
	default:
		return markPending
	}
}

func (h *human) phase(op *pb.Operation) string {
	return h.Mark(opMark(op)) + " " + cmp.Or(op.GetPhase(), opPhaseWaiting)
}

// opTarget is what an operation acts on: the node of a node action, else its cluster.
func opTarget(op *pb.Operation) string {
	if op.GetNodeMachine() != nil {
		return op.GetNodeMachine().GetName()
	}

	return op.GetCluster().GetName()
}

func renderOperationList(h *human, resp *pb.ListOperationsResponse) error {
	ops := resp.GetOperations()
	if len(ops) == 0 {
		h.Note("no operations")

		return h.Err()
	}

	namespaces := map[string]bool{}
	for _, op := range ops {
		namespaces[op.GetRef().GetNamespace()] = true
	}

	header := []string{opColName, "kind", "phase", "target", "message", opColAge}
	if len(namespaces) > 1 {
		header = slices.Insert(header, 0, "namespace")
	}

	rows := make([][]string, len(ops))

	for i, op := range ops {
		row := []string{op.GetRef().GetName(), kindName(op.GetKind()), h.phase(op), opTarget(op),
			cmp.Or(op.GetFailureReason(), op.GetMessage()), humanAge(op.GetCreatedAt())}
		if len(namespaces) > 1 {
			row = slices.Insert(row, 0, op.GetRef().GetNamespace())
		}

		rows[i] = row
	}

	h.Table(header, rows)

	return h.Err()
}

// opStage is the stage or step an operation is in: a CA rotation or Kubernetes stage, an etcd restore step.
func opStage(op *pb.Operation) string {
	return cmp.Or(op.GetCaRotationStage(), op.GetKubernetesStage(), op.GetEtcdRestoreStep())
}

// opNote is what a running operation says it is doing: its message, else its node action's or etcd restore's.
func opNote(op *pb.Operation) string {
	return cmp.Or(op.GetMessage(), op.GetNodeMessage(), op.GetEtcdRestoreMessage())
}

func opHead(h *human, op *pb.Operation) []kv {
	return []kv{
		{opLabelKind, kindName(op.GetKind())}, {"Summary", op.GetSummary()}, {opLabelCluster, op.GetCluster().GetName()},
		{opLabelPhase, h.phase(op)}, {"Stage", opStage(op)}, {"Message", opNote(op)}, {"Failure", op.GetFailureReason()},
		{condPaused, yesIf(op.GetPaused() && op.GetPhase() != condPaused)},
	}
}

func renderOperationGet(h *human, resp *pb.GetOperationResponse) error {
	op := resp.GetOperation()

	rows := opHead(h, op)
	rows = append(rows, kv{"Started by", op.GetStartedBy()}, kv{opLabelCreated, agoOrEmpty(op.GetCreatedAt())},
		kv{"Finished", agoOrEmpty(op.GetFinishedAt())})
	h.Block(nsName(op.GetRef()), rows...)

	details := make([]kv, 0, len(op.GetDetails()))

	for _, d := range op.GetDetails() {
		if d.GetKey() != opLabelCluster && d.GetKey() != opLabelKind { // shown above
			details = append(details, kv{d.GetKey(), d.GetValue()})
		}
	}

	h.Block("Details", details...)
	renderOperationMachines(h, op)
	h.Conditions("Conditions", conditionsOf(op.GetConditions()))

	return h.Err()
}

func renderOperationMachines(h *human, op *pb.Operation) {
	ms := op.GetMachines()
	if len(ms) == 0 {
		return
	}

	named := slices.ContainsFunc(ms, func(m *pb.OperationMachine) bool { return m.GetDisplayName() != "" })
	header := []string{opColMachine, "phase", "step", "time", "message"}

	if named {
		header = slices.Insert(header, 1, colNode)
	}

	rows := make([][]string, len(ms))

	for i, m := range ms {
		mk := markPending
		switch {
		case m.GetPhase() == ichorv1alpha1.TalosOperationMachinePhaseCompleted:
			mk = markOK
		case opFailed(op) && m.GetPhase() == ichorv1alpha1.TalosOperationMachinePhaseInProgress:
			mk = markFailed
		}

		row := []string{m.GetName(), h.Mark(mk) + " " + m.GetPhase(), m.GetStep(), machineTime(m), m.GetMessage()}
		if named {
			row = slices.Insert(row, 1, m.GetDisplayName())
		}

		rows[i] = row
	}

	h.Title("Machines")
	h.Table(header, rows)
}

// machineTime is how long a machine's step took, or has run so far.
func machineTime(m *pb.OperationMachine) string {
	if m.GetStartedAt() == nil {
		return ""
	}

	if m.GetFinishedAt() != nil {
		return formatElapsed(m.GetFinishedAt().AsTime().Sub(m.GetStartedAt().AsTime()))
	}

	return formatElapsed(humanNow().Sub(m.GetStartedAt().AsTime()))
}

// renderOperationCreated is the result of a start: the operation and how to follow it.
func renderOperationCreated(h *human, resp *pb.CreateOperationResponse) error {
	op := resp.GetOperation()

	rows := opHead(h, op)
	rows = append(rows, kv{"Spec", changedNote(resp.GetSpecChanged())},
		kv{"Follow", "ichor operation get " + op.GetRef().GetName() + " --watch"})
	h.Block("Started "+nsName(op.GetRef()), rows...)

	return h.Err()
}

func changedNote(changed bool) string {
	if changed {
		return "changed to the target first"
	}

	return ""
}

// renderOperationPaused is the result of pause and resume.
func renderOperationPaused(h *human, resp *pb.SetOperationPausedResponse) error {
	op := resp.GetOperation()
	state := "resumed"

	if op.GetPaused() {
		state = "paused"
	}

	h.Block(nsName(op.GetRef()), append([]kv{{"State", state}}, opHead(h, op)...)...)

	return h.Err()
}

// opWatch is the live view of a followed operation: a title, then one step that carries the phase
// and message as its detail and ends ✓, ✗ or !, with the time the operation took.
type opWatch struct {
	p     *progress
	st    *step
	stage string // the last CA rotation stage seen, so that a stage a poll jumped over is shown too
}

// watchClock is the clock of the live view; tests replace it.
var watchClock = time.Now

func newOpWatch(errw io.Writer) *opWatch {
	p := newProgress(errw)
	p.now = watchClock
	p.began = p.now()

	return &opWatch{p: p}
}

// detail is what the operation is doing, in one line.
func (w *opWatch) detail(op *pb.Operation, stage string) string {
	return w.detailIn(op, stage, cmp.Or(op.GetPhase(), opPhaseWaiting))
}

// detailIn is detail with the phase given: a stage a poll jumped over was passed while the operation ran.
func (w *opWatch) detailIn(op *pb.Operation, stage, phase string) string {
	parts := []string{phase}

	if n := len(op.GetMachines()); n > 0 {
		done := 0

		for _, m := range op.GetMachines() {
			if m.GetPhase() == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
				done++
			}
		}

		parts = append(parts, fmt.Sprintf("%d/%d machines", done, n))
	}

	if stage = cmp.Or(stage, op.GetKubernetesStage(), op.GetEtcdRestoreStep()); stage != "" {
		parts = append(parts, "stage "+stage)
	}

	switch {
	case op.GetPhase() == condPaused && !opFailed(op):
		parts = append(parts, "resume with `ichor operation resume "+op.GetRef().GetName()+"`")
	case opFailed(op):
		parts = append(parts, op.GetFailureReason())
	default:
		if n := opNote(op); n != "" {
			parts = append(parts, n)
		}
	}

	return strings.Join(parts, " · ")
}

// show starts the step on the first poll and sets its detail; a finished or failed operation is
// left to end.
func (w *opWatch) show(op *pb.Operation) {
	if w.st == nil {
		w.p.Header(cmp.Or(op.GetSummary(), "Operation "+kindName(op.GetKind())),
			"operation "+op.GetRef().GetName()+" · cluster "+op.GetCluster().GetName())
		w.st = w.p.Start(kindName(op.GetKind()))
		w.began(op)
	}

	stage := op.GetCaRotationStage()
	for _, s := range passedStages(w.stage, stage) {
		w.st.Update(w.detailIn(op, s, ichorv1alpha1.TalosOperationPhaseInProgress))
	}

	w.stage = stage

	if !opFinished(op) && !opFailed(op) { // the end line says how it ended
		w.st.Update(w.detail(op, stage))
	}
}

// began makes the step's time the operation's own: from its creation, or for a finished one how
// long it took.
func (w *opWatch) began(op *pb.Operation) {
	created := op.GetCreatedAt().AsTime()
	if op.GetCreatedAt() == nil || created.IsZero() {
		return
	}

	start := created
	if f := op.GetFinishedAt(); f != nil {
		start = w.p.now().Add(-f.AsTime().Sub(created))
	}

	w.p.mu.Lock()
	w.st.began = start
	w.p.mu.Unlock()
}

// end closes the step for a finished or failed operation.
func (w *opWatch) end(op *pb.Operation) {
	switch {
	case opFailed(op):
		w.st.end(markFail, sgrRed, "Failed", nil)
	case op.GetPhase() == ichorv1alpha1.TalosOperationPhaseShutDown:
		w.st.Done("Shut down")
	default:
		w.st.Done(ichorv1alpha1.TalosOperationPhaseCompleted)
	}
}

// abort ends the step when the follow stops on err: Ctrl-C leaves the operation running.
func (w *opWatch) abort(err error, interrupted bool) error {
	if w.st == nil || w.st.isEnded() {
		return err
	}

	if interrupted {
		w.st.Warn("Stopped following; the operation keeps running")

		return err
	}

	return w.st.Fail(err)
}

// opReporter is how a poll loop says what it sees. The machine formats print today's stderr lines;
// the human one is opWatch.
type opReporter struct {
	w     *opWatch // nil for -o yaml|json
	lines stageProgress
	errw  io.Writer
}

func newOpReporter(errw io.Writer) *opReporter {
	if outputFormat == outputHuman {
		return &opReporter{w: newOpWatch(errw), errw: errw}
	}

	return &opReporter{lines: stageProgress{w: errw}, errw: errw}
}

// retryWriter is where "API unreachable, retrying" goes.
func (r *opReporter) retryWriter() io.Writer {
	if r.w != nil {
		return r.w.p.WarnWriter()
	}

	return r.errw
}

func (r *opReporter) gone() {
	if r.w != nil {
		r.w.p.Warn(opGoneMessage)

		return
	}

	_, _ = fmt.Fprintln(r.errw, "ichor: "+opGoneMessage)
}

func (r *opReporter) show(op *pb.Operation) {
	if r.w != nil {
		r.w.show(op)

		return
	}

	r.lines.show(op.GetCaRotationStage(), func(stage string) string { return progressLineAt(op, stage) })
}

func (r *opReporter) end(op *pb.Operation) {
	if r.w != nil {
		r.w.end(op)
	}
}

func (r *opReporter) abort(err error, interrupted bool) error {
	if r.w != nil {
		return r.w.abort(err, interrupted)
	}

	return err
}

// announceCreated says on errw that a start created the operation, before the follow begins. The
// human follow's title names the operation itself.
func announceCreated(errw io.Writer, name string) {
	if outputFormat == outputHuman {
		return
	}

	_, _ = fmt.Fprintf(errw, "created operation %s\n", name)
}

// rerunNotice says that resuming also re-runs the step that failed.
func rerunNotice(errw io.Writer, reason string) {
	if outputFormat != outputHuman {
		_, _ = fmt.Fprintf(errw, "ichor: re-running the step that failed: %s\n", reason)

		return
	}

	h := newHuman(errw)
	h.Note(h.Mark(markWarning) + " re-running the step that failed: " + reason)
}
