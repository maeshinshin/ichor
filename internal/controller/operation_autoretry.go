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
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/transient"
)

// operationAutoRetryLimit is how many times the controller retries one failed step by itself (ADR 0174).
const operationAutoRetryLimit = 5

// operationRetryBase is the wait before the first automatic retry; each further one doubles it
// (cniRetryBackoff). A variable so tests can shrink it.
var operationRetryBase = time.Minute

// sendUnknownError marks the error of a call that may have reached the node before it failed, so
// running the step again could send it twice. Such a step is never retried automatically.
type sendUnknownError struct{ err error }

func (e *sendUnknownError) Error() string { return e.err.Error() }
func (e *sendUnknownError) Unwrap() error { return e.err }

// nodeStepRetryable is whether a failed node action may be retried: not a reset, and not one whose
// request was already sent (a retry would send it again).
func nodeStepRetryable(op *ichorv1alpha1.TalosOperation) bool {
	return op.Spec.Node != nil && op.Spec.Node.Action != ichorv1alpha1.NodeActionReset &&
		(op.Status.Node == nil || op.Status.Node.Step != ichorv1alpha1.NodeStepRequested)
}

// stepRetryable is whether err is a failure a later attempt may not meet and that is safe to run again.
func stepRetryable(err error) bool {
	_, sent := errors.AsType[*sendUnknownError](err)

	return transient.Is(err) && !sent
}

// pauseFailedStep pauses op on a failed step. When the failure is retryable and step has attempts left, it
// schedules the next attempt (status.autoRetry.nextAt) and requeues for it; otherwise the pause waits for a
// person, saying so when the retries ran out.
func (r *TalosOperationReconciler) pauseFailedStep(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, step string, err error, retryable bool, message string,
) (ctrl.Result, error) {
	st := op.Status.AutoRetry
	if st == nil || st.Step != step {
		st = &ichorv1alpha1.TalosOperationAutoRetryStatus{Step: step}
	}

	st.NextAt = nil

	var res ctrl.Result

	switch {
	case retryable && stepRetryable(err) && st.Attempts < operationAutoRetryLimit:
		wait := cniRetryBackoff(operationRetryBase, st.Attempts+1)
		next := metav1.NewTime(time.Now().Add(wait))
		st.NextAt = &next
		res.RequeueAfter = wait
		message += fmt.Sprintf(". Retrying automatically at %s (attempt %d of %d)",
			next.UTC().Format(time.RFC3339), st.Attempts+1, operationAutoRetryLimit)
	case st.Attempts > 0:
		message += fmt.Sprintf(". Automatic retry gave up after %d attempts", st.Attempts)
	}

	op.Status.AutoRetry = nil
	if st.Attempts > 0 || st.NextAt != nil {
		op.Status.AutoRetry = st
	}

	return res, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationStepFailed, message)
}

// autoRetryDue runs the automatic retry of a failed step once its time has come, as a person's retry request
// does. Until then it asks for a reconcile at that time. It reports whether the step was put back to run.
func (r *TalosOperationReconciler) autoRetryDue(ctx context.Context, op *ichorv1alpha1.TalosOperation) (ctrl.Result, bool, error) {
	st := op.Status.AutoRetry
	if st == nil || st.NextAt == nil {
		return ctrl.Result{}, false, nil
	}

	if wait := time.Until(st.NextAt.Time); wait > 0 {
		return ctrl.Result{RequeueAfter: wait}, false, nil
	}

	st.Attempts++
	st.NextAt = nil

	return ctrl.Result{}, true, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhaseInProgress, reasonOperationAdvancing,
		fmt.Sprintf("retrying the failed step automatically (attempt %d of %d)", st.Attempts, operationAutoRetryLimit))
}
