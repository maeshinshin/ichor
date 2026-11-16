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
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// startupWindow is how long after the controller starts a webhook call that fails is expected. On a
// fresh install the controller's own admission webhook is not callable for about fifteen seconds
// (certificate bootstrap, then the Service's endpoint waiting on a 10 s readiness probe), and the
// controller writes to resources that webhook guards in that time. A restart repeats it.
//
// The window is elapsed time on purpose, and the controller does not wait for its own webhook before
// reconciling: a component waiting on itself turns fifteen seconds of noise into a deadlock when
// the webhook never comes up. A "webhook is up" flag would be worse here, because it reads "starting"
// forever when the webhook never comes up, which hides a real outage; a window ends, and after it
// the failure is returned and logged as the error it then is.
const startupWindow = 2 * time.Minute

// processStart is when this process began, the start of its startup window.
var processStart = time.Now()

// quietExpected wraps r so two errors that are routine do not read as failures. controller-runtime
// logs every error a reconciler returns at error level and nothing else, so the wrapper logs them
// itself at info level, with the reason they are expected, and asks for the same rate-limited retry
// the error would have got.
//
//   - A conflict ("the object has been modified") is optimistic concurrency working: the write
//     lost to a newer version, and the next reconcile reads that version. It is expected at any time.
//   - A webhook that cannot be called ("failed calling webhook") is expected only within
//     startupWindow of the process start. After that it is returned unchanged, and so logged as an
//     error, because by then it is a real problem.
//
// shortcut: a conflict that never resolves is logged at info level on every retry and no longer counts in
// controller_runtime_reconcile_errors_total (IchorReconcileErrorsPersisting); count consecutive
// conflicts per request and return the error past a limit if such a loop is ever seen.
func quietExpected(r reconcile.Reconciler) reconcile.Reconciler {
	return quietExpectedSince(r, processStart, time.Now)
}

func quietExpectedSince(r reconcile.Reconciler, start time.Time, now func() time.Time) reconcile.Reconciler {
	return reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
		res, err := r.Reconcile(ctx, req)

		switch {
		case err == nil:
			return res, nil
		case apierrors.IsConflict(err):
			logf.FromContext(ctx).Info("Write conflicted with a newer version of the object; the reconcile runs again (expected)",
				"error", err.Error())
		case unreachableWebhook(err) && now().Sub(start) < startupWindow:
			logf.FromContext(ctx).Info("Admission webhook not reachable yet; the reconcile runs again (expected while it starts, "+
				"an error after the startup window)", "error", err.Error(), "startupWindow", startupWindow.String())
		default:
			return res, err
		}

		return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // the rate-limited retry the returned error would have got
	})
}

// unreachableWebhook reports whether err is the API server failing to call an admission webhook, which
// arrives as a 500 InternalError "failed calling webhook ...: <cause>" whatever the cause. A webhook
// that answered and refused is Forbidden or Invalid, and stays an answer.
func unreachableWebhook(err error) bool {
	return strings.Contains(err.Error(), "failed calling webhook") && !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err)
}
