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

package uiapi

import (
	"context"
	"crypto/rand"
	"errors"
	"log"

	"connectrpc.com/connect"
)

// SanitizeErrors is the outermost interceptor: an error whose code says "the server failed"
// (internal, unknown, data loss, unavailable) wraps raw API server, Talos or dial errors, which
// carry resource paths, ServiceAccount names and node addresses. The browser gets the code and a
// reference; the full error goes to the log under the same reference (docs/adr/0108). Errors
// the caller caused (not found, forbidden, invalid argument ...) pass unchanged.
func SanitizeErrors(l *log.Logger) connect.Interceptor { return sanitizer{l: l} }

type sanitizer struct {
	serverOnly

	l *log.Logger
}

func (s sanitizer) clean(procedure string, err error) error {
	if err == nil {
		return nil
	}

	var ce *connect.Error
	if !errors.As(err, &ce) {
		ce = connect.NewError(connect.CodeUnknown, err)
	}

	switch ce.Code() {
	case connect.CodeInternal, connect.CodeUnknown, connect.CodeDataLoss, connect.CodeUnavailable:
	default:
		return err
	}

	ref := rand.Text()[:8]
	s.l.Printf("rpc %s failed ref=%s code=%s: %v", procedure, ref, ce.Code(), err)

	out := connect.NewError(ce.Code(), errors.New("the server could not complete the request (reference "+ref+"; details are in the ichor-ui log)"))

	return out
}

func (s sanitizer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		res, err := next(ctx, req)

		return res, s.clean(req.Spec().Procedure, err)
	}
}

func (s sanitizer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return s.clean(conn.Spec().Procedure, next(ctx, conn))
	}
}
