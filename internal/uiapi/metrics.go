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
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// rpcDuration is the one metric the UI backend adds (docs/design/architecture.md,
// "Monitoring and registry mirrors": "the controller and UI backend export Prometheus
// metrics"): latency and
// outcome for every Connect RPC it serves, by procedure and status code. It is
// observed from one place -- MetricsInterceptor below -- rather than timing code
// hand-written into each of ClusterService/MachineService/NodeService/PatchService's
// handlers, because connectrpc.com/connect v1.21.0 already ships an interceptor
// mechanism built exactly for this (connect.Interceptor, registered once per handler
// via connect.WithInterceptors in cmd/ichor-ui/main.go). Error rate is read off this
// histogram's non-"ok" code buckets, not tracked as a second counter: the code label
// already carries that signal, and a count-only series duplicating part of a histogram
// that already has _count would be the thing not worth adding.
var rpcDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name: "ichor_uiapi_rpc_duration_seconds",
	Help: "Duration in seconds of ichor-ui Connect RPCs, by procedure and outcome code.",
}, []string{"procedure", "code"})

// MetricsInterceptor times every RPC ichor-ui serves and records it under
// rpcDuration. Pass it to connect.WithInterceptors for every *ServiceHandler
// constructor in cmd/ichor-ui/main.go.
func MetricsInterceptor() connect.Interceptor {
	return metricsInterceptor{}
}

type metricsInterceptor struct{ serverOnly }

func (metricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		res, err := next(ctx, req)
		observeRPC(req.Spec().Procedure, err, time.Since(start))
		return res, err
	}
}

func (metricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		start := time.Now()
		err := next(ctx, conn)
		observeRPC(conn.Spec().Procedure, err, time.Since(start))
		return err
	}
}

// observeRPC records one RPC's outcome. code is "ok" on success, otherwise the
// connect.Code name (e.g. "unavailable", "not_found") -- NodeService.StreamNodeLogs's
// own CodeUnavailable on shutdown (cmd/ichor-ui/main.go's "draining" channel) lands
// here the same way any other RPC's outcome does.
func observeRPC(procedure string, err error, d time.Duration) {
	code := "ok"
	if err != nil {
		code = connect.CodeOf(err).String()
	}
	rpcDuration.WithLabelValues(procedure, code).Observe(d.Seconds())
}
