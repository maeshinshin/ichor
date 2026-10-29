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

// Command reclaimui calls ichor-ui's ReclaimService for the hardware cases (C8-C10): the CLI has no
// reclaim-create for the UI path. It runs inside the cluster network (ichor-ui's certificate is self-signed, so
// verification is skipped) and prints the response as JSON. It never prints the talosconfig it sends.
//
//	reclaimui -url https://HOST:8443 can [-cluster NAME]
//	reclaimui -url ... create -address IP -talosconfig FILE [-cluster NAME] [-force] [-force-ungraceful]
//	reclaimui -url ... get|confirm|list ...
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "reclaimui:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("reclaimui", flag.ExitOnError)
	url := fs.String("url", "https://ichor-ichor-ui-service.ichor-system.svc:8443", "ichor-ui base URL")
	token := fs.String("token", "", "bearer token, when users are on")
	address := fs.String("address", "", "create: node address")
	tc := fs.String("talosconfig", "", "create: talosconfig file")
	tcContext := fs.String("context", "", "create: talosconfig context")
	cluster := fs.String("cluster", "", "target TalosCluster name (namespace ichor-system)")
	name := fs.String("name", "", "get, confirm: MachineReclaim name")
	uuid := fs.String("uuid", "", "confirm: typed UUID")
	clusterName := fs.String("confirm-cluster", "", "confirm: second typed confirmation")
	force := fs.Bool("force", false, "create: force")
	ungraceful := fs.Bool("force-ungraceful", false, "create: force-ungraceful")

	if len(os.Args) < 2 {
		return fmt.Errorf("usage: reclaimui [-url U] can|create|get|confirm|list [flags]")
	}

	op := os.Args[1]
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	// The in-cluster test endpoint is self-signed.
	tlsCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test harness only
	hc := &http.Client{Timeout: time.Minute, Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	opts := []connect.ClientOption{}
	if *token != "" {
		bearer := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Authorization", "Bearer "+*token)
				return next(ctx, r)
			}
		})
		opts = append(opts, connect.WithInterceptors(bearer))
	}

	c := ichorv1alpha1connect.NewReclaimServiceClient(hc, *url, opts...)
	ctx := context.Background()
	ref := func(n string) *pb.ObjectRef { return &pb.ObjectRef{Namespace: "ichor-system", Name: n} }

	var clusterRef *pb.ObjectRef
	if *cluster != "" {
		clusterRef = ref(*cluster)
	}

	var (
		out proto.Message
		err error
	)

	switch op {
	case "can":
		out, err = msg(c.CanReclaim(ctx, connect.NewRequest(&pb.CanReclaimRequest{Cluster: clusterRef})))
	case "create":
		raw, rerr := os.ReadFile(*tc)
		if rerr != nil {
			return fmt.Errorf("reading -talosconfig: %w", rerr)
		}

		out, err = msg(c.CreateReclaim(ctx, connect.NewRequest(&pb.CreateReclaimRequest{
			Address: *address, Talosconfig: raw, Context: *tcContext,
			Force: *force, ForceUngraceful: *ungraceful, Cluster: clusterRef,
		})))
	case "get":
		out, err = msg(c.GetReclaim(ctx, connect.NewRequest(&pb.GetReclaimRequest{Reclaim: ref(*name)})))
	case "list":
		out, err = msg(c.ListReclaims(ctx, connect.NewRequest(&pb.ListReclaimsRequest{})))
	case "confirm":
		out, err = msg(c.ConfirmReclaim(ctx, connect.NewRequest(&pb.ConfirmReclaimRequest{
			Reclaim: ref(*name), Uuid: *uuid, ClusterName: *clusterName,
		})))
	default:
		return fmt.Errorf("unknown operation %q", op)
	}

	if err != nil {
		return err
	}

	fmt.Println(protojson.Format(out))

	return nil
}

// msg unwraps a unary response into its message.
func msg[T any, P interface {
	*T
	proto.Message
}](r *connect.Response[T], err error) (proto.Message, error) {
	if err != nil {
		return nil, err
	}
	return P(r.Msg), nil
}
