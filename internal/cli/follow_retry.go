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
	"net"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// followRetryWindow bounds how long a --follow/--watch poll keeps retrying
// one unreachable API (a single control plane rebooting takes a few minutes);
// followRetryDelay is the first backoff step, doubled up to followRetryMax.
// Vars so tests can shrink them.
var (
	followRetryWindow = 10 * time.Minute
	followRetryDelay  = time.Second
	followRetryMax    = 15 * time.Second
)

// isTransient reports whether err is the API being briefly unreachable --
// connection refused/reset, Unavailable, EOF, a timeout, discovery not yet
// serving a group -- as opposed to an
// answer (NotFound, PermissionDenied, ...) that retrying cannot change.
func isTransient(err error) bool {
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

// sleepCtx waits d, or returns ctx.Err() as soon as ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// retryTransient runs call, retrying transient errors with backoff for up to
// followRetryWindow and printing one "API unreachable, retrying" line per
// attempt. A dead ichor-ui tunnel is re-opened through c.Reconnect. It returns
// the last error once the window is spent, or at once for a non-transient one.
// It is the one retry used by every --follow/--watch path.
func retryTransient(ctx context.Context, c *apiClients, errw io.Writer, call func() error) error {
	deadline := time.Now().Add(followRetryWindow)
	delay := followRetryDelay

	var movedTo string // the apiserver tried after the first one failed

	for {
		err := call()
		if !isTransient(err) || ctx.Err() != nil {
			if movedTo != "" && (err == nil || !isTransient(err)) {
				_, _ = fmt.Fprintf(errw, "ichor: API server %s answered\n", movedTo)
			}

			return err
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("API still unreachable after %s: %w", followRetryWindow, err)
		}

		_, _ = fmt.Fprintf(errw, "ichor: API unreachable, retrying (%v)\n", err)

		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}

		delay = min(delay*2, followRetryMax)

		if c.Failover != nil {
			if name, nc, ferr := c.Failover(ctx); ferr == nil {
				keep := *c
				*c = *nc
				c.Close, c.Reconnect, c.Failover, c.Dial = keep.Close, keep.Reconnect, keep.Failover, keep.Dial
				c.actor = keep.actor
				movedTo = name

				_, _ = fmt.Fprintf(errw, "ichor: trying API server %s\n", name)
			}
		} else if c.Reconnect != nil {
			if nc, rerr := c.Reconnect(ctx); rerr == nil {
				closeFn, reconnect := c.Close, c.Reconnect
				*c = *nc
				c.Close, c.Reconnect = closeFn, reconnect
			} else {
				// Say why the tunnel could not be rebuilt (a kubeconfig the cluster no longer
				// accepts, no ready ichor-ui pod): silence here hid the cause of a stuck follow.
				_, _ = fmt.Fprintf(errw, "ichor: could not re-open the tunnel to ichor-ui (%v)\n", rerr)
			}
		}
	}
}
