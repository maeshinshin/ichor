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

// Package transient says whether an error is a failure that clears on its own: the API or a node briefly
// unreachable, as opposed to an answer a retry cannot change. The CLI's polls and the controller's automatic
// retry of a paused operation (ADR 0174) share this one classification.
package transient

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// Is reports whether err is the API being briefly unreachable --
// connection refused/reset, Unavailable, EOF, a timeout, discovery not yet
// serving a group -- as opposed to an
// answer (NotFound, PermissionDenied, ...) that retrying cannot change.
func Is(err error) bool {
	if err == nil {
		return false
	}

	if ce, ok := errors.AsType[*connect.Error](err); ok {
		switch ce.Code() {
		case connect.CodeUnavailable, connect.CodeDeadlineExceeded:
			return true
		case connect.CodeUnknown, connect.CodeInternal:
			// Fall through to the cause text: a dropped connection can arrive wrapped.
		default:
			return false
		}
	}

	var (
		ne        net.Error
		restmap   *apiutil.ErrResourceDiscoveryFailed
		groupdisc *discovery.ErrGroupDiscoveryFailed
	)

	switch {
	// Discovery failing while a restarted kube-apiserver has not yet served the CRD groups. The client's
	// lazy REST mapper does not cache the failure, so the same client succeeds on the next attempt.
	case meta.IsNoMatchError(err), errors.As(err, &restmap), errors.As(err, &groupdisc), discovery.IsGroupDiscoveryFailedError(err):
		return true
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout(),
		apierrors.IsServerTimeout(err), apierrors.IsTimeout(err), apierrors.IsServiceUnavailable(err), apierrors.IsTooManyRequests(err):
		return true
	}

	msg := err.Error()

	// An admission webhook that cannot be called (its pod restarts with the control plane it runs on)
	// arrives as a 500 InternalError "failed calling webhook ...: <cause>", whatever the cause. A webhook
	// that answered and refused is Forbidden or Invalid, and stays an answer.
	if strings.Contains(msg, "failed calling webhook") && !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
		return true
	}

	// etcd timing out behind a healthy apiserver arrives as a 500 InternalError carrying its text.
	return strings.Contains(msg, "etcdserver: request timed out") || strings.Contains(msg, "etcdserver: leader changed") ||
		strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "unexpected EOF") || strings.HasSuffix(msg, ": EOF") || strings.Contains(msg, "i/o timeout")
}
