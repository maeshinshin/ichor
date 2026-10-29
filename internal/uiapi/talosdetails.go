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
	"sync"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	// talosDetailsTimeout bounds each section's Talos call on its own, so one
	// hung call shows an inline error while the others still render.
	talosDetailsTimeout = 10 * time.Second
	maxSectionErrorLen  = 300
)

// GetNodeTalosDetails reads one machine's Talos-side facts, read-only. Authorization is the same as GetNode: the
// machine and
// cluster are read as the caller (getMachineAndCluster), so a caller who may
// not see the TalosMachine gets no Talos data either. Sections run
// concurrently and fail independently: a failed section carries its error
// message and the call still succeeds.
func (s *Server) GetNodeTalosDetails(
	ctx context.Context, req *connect.Request[pb.GetNodeTalosDetailsRequest],
) (*connect.Response[pb.GetNodeTalosDetailsResponse], error) {
	_, tc, closeFn, maintenance, err := s.dialMachine(ctx, req.Msg.GetMachine())
	if err != nil {
		return nil, err
	}

	defer closeFn()

	// A machine without CA trust (not approved, or approved but still in
	// maintenance mode) is read over the insecure maintenance API, which
	// allows fewer calls (dialNode).
	resp := &pb.GetNodeTalosDetailsResponse{MaintenanceMode: maintenance}

	var wg sync.WaitGroup

	run := func(read func(context.Context)) {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, talosDetailsTimeout)
			defer cancel()

			read(cctx)
		})
	}

	run(func(c context.Context) {
		resp.System = section(c, tc.TalosSystem, func(e string) *pb.TalosSystemSection { return &pb.TalosSystemSection{Error: e} })
		resp.System.KernelArgs = redactKernelArgs(resp.System.KernelArgs)
	})
	run(func(c context.Context) {
		resp.Network = section(c, tc.TalosNetwork, func(e string) *pb.TalosNetworkSection { return &pb.TalosNetworkSection{Error: e} })

	})
	run(func(c context.Context) {
		resp.Storage = section(c, tc.TalosStorage, func(e string) *pb.TalosStorageSection { return &pb.TalosStorageSection{Error: e} })

	})
	run(func(c context.Context) {
		resp.Services = section(c, tc.TalosServices, func(e string) *pb.TalosServicesSection { return &pb.TalosServicesSection{Error: e} })

	})
	run(func(c context.Context) {
		resp.Extensions = section(c, tc.TalosExtensions, func(e string) *pb.TalosExtensionsSection { return &pb.TalosExtensionsSection{Error: e} })

	})

	wg.Wait()

	return connect.NewResponse(resp), nil
}

// section runs one section read and turns a failure (or a nil result) into a
// section carrying only the error text, never partial data.
func section[T any](ctx context.Context, read func(context.Context) (*T, error), failed func(string) *T) *T {
	v, err := read(ctx)
	if err != nil {
		msg := err.Error()
		if len(msg) > maxSectionErrorLen {
			msg = msg[:maxSectionErrorLen] + "..."
		}

		return failed(msg)
	}

	if v == nil {
		return failed("")
	}

	return v
}
