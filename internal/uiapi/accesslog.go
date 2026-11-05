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
	"log"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// reclaimPrefix selects every ReclaimService procedure by name (ADR 0103, 2.2): the request of CreateReclaim carries a
// foreign Talos admin credential, so no body of this service is ever logged.
const reclaimPrefix = "/" + ichorv1alpha1connect.ReclaimServiceName + "/"

// AccessLogInterceptor logs one line per unary RPC: procedure, outcome and duration. With logBodies it adds the
// request message, except for the reclaim methods, whose body is dropped by method name.
func AccessLogInterceptor(l *log.Logger, logBodies bool) connect.Interceptor {
	return accessLog{l: l, bodies: logBodies}
}

type accessLog struct {
	serverOnly

	l      *log.Logger
	bodies bool
}

func (a accessLog) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		res, err := next(ctx, req)

		body := ""
		if m, ok := req.Any().(proto.Message); ok && a.bodies && !strings.HasPrefix(req.Spec().Procedure, reclaimPrefix) {
			body = " body=" + prototext.MarshalOptions{}.Format(m)
		}

		a.l.Printf("rpc %s code=%s dur=%s%s", req.Spec().Procedure, rpcCode(err), time.Since(start).Round(time.Millisecond), body)

		return res, err
	}
}

func (a accessLog) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		err := next(ctx, conn)

		a.l.Printf("rpc %s code=%s", conn.Spec().Procedure, rpcCode(err))

		return err
	}
}
