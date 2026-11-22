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
	"fmt"
	"io"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/maeshinshin/ichor/internal/transient"
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
	return retryTransientOr(ctx, c, errw, nil, call)
}

// isImpersonationDenied is ichor-ui answering that its own ServiceAccount may not impersonate the
// caller: what a request meets while an Ichor upgrade replaces ichor-ui's RBAC and ServiceAccount.
func isImpersonationDenied(err error) bool {
	return connect.CodeOf(err) == connect.CodePermissionDenied && strings.Contains(err.Error(), "cannot impersonate")
}

// retryTransientOr is retryTransient that also retries the errors orElse accepts, within the same
// window; a nil orElse accepts none.
func retryTransientOr(ctx context.Context, c *apiClients, errw io.Writer, orElse func(error) bool, call func() error) error {
	retryable := func(err error) bool { return transient.Is(err) || orElse != nil && orElse(err) }
	deadline := time.Now().Add(followRetryWindow)
	delay := followRetryDelay

	var movedTo string // the apiserver tried after the first one failed

	for {
		err := call()
		if !retryable(err) || ctx.Err() != nil {
			if movedTo != "" && (err == nil || !retryable(err)) {
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
