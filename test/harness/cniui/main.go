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

// Command cniui calls ichor-ui's ClusterService.GetCluster and CniService for the ADR 0103 hardware case D6 (the UI
// API on a CNI-unmanaged cluster). It reaches ichor-ui through the port-forward `ichor ui` opens (the certificate is
// self-signed, so verification is skipped) and prints each response, or the error with its Connect code, as one line.
//
//	cniui -url https://127.0.0.1:PORT -cluster NAME
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
	url := flag.String("url", "https://127.0.0.1:8443", "ichor-ui base URL")
	cluster := flag.String("cluster", "", "TalosCluster name (namespace ichor-system)")
	flag.Parse()

	tlsCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test harness only
	hc := &http.Client{Timeout: time.Minute, Transport: &http.Transport{TLSClientConfig: tlsCfg}}
	ref := &pb.ObjectRef{Namespace: "ichor-system", Name: *cluster}
	ctx := context.Background()
	cl := ichorv1alpha1connect.NewClusterServiceClient(hc, *url)
	cni := ichorv1alpha1connect.NewCniServiceClient(hc, *url)

	show := func(name string, m proto.Message, err error) {
		if err != nil {
			fmt.Printf("%s: ERROR %s\n", name, err)
			return
		}
		fmt.Printf("%s: OK %s\n", name, protojson.MarshalOptions{}.Format(m))
	}
	r1, err := cl.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{Cluster: ref}))
	show("GetCluster", msg(r1), err)
	r2, err := cni.GetCiliumValues(ctx, connect.NewRequest(&pb.GetCiliumValuesRequest{Cluster: ref}))
	show("GetCiliumValues", msg(r2), err)
	r3, err := cni.SetCiliumValues(ctx, connect.NewRequest(&pb.SetCiliumValuesRequest{
		Cluster: ref, UserYaml: "debug:\n  enabled: true\n",
	}))
	show("SetCiliumValues", msg(r3), err)
	r4, err := cni.StartCniUpgrade(ctx, connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
	show("StartCniUpgrade", msg(r4), err)
	_ = os.Stdout.Sync()
}

// msg returns the response message, or nil when the call failed (r is nil then).
func msg[T any, P interface {
	*T
	proto.Message
}](r *connect.Response[T]) proto.Message {
	if r == nil {
		return nil
	}
	return P(r.Msg)
}
