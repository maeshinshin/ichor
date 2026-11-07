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
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// logsReconnectDelay is the pause before --follow reconnects. A var so
// tests can shrink it.
var logsReconnectDelay = 2 * time.Second

var nodeLogsOpts nodeLogsOptions

type nodeLogsOptions struct {
	Service     string
	Follow      bool
	ResumeToken string
	Idle        time.Duration
}

// nodeCmd groups the subcommands that read a live node. They need
// ichor-ui: the read-only Talos identity lives there and the CLI never
// talks to a node itself, so there is no local fallback.
var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Show a node, tail its logs (needs ichor-ui), or reboot, shut down or remove it",
}

var nodeGetCmd = &cobra.Command{
	Use:   useGetName,
	Short: "Show a node's hardware, disks, network interfaces, usage and Kubernetes status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return showAPIHuman(cmd, true, func(ctx context.Context, c *apiClients) (*connect.Response[pb.GetNodeResponse], error) {
			return c.Node.GetNode(ctx, connect.NewRequest(&pb.GetNodeRequest{Machine: objectRef(apiOpts.Namespace, args[0])}))
		}, renderNodeGet)
	},
}

var nodeLogsCmd = &cobra.Command{
	Use:   "logs NAME...",
	Short: "Print a node's kernel or service log; --follow keeps streaming",
	Long: `logs prints the kernel log (or, with --service, a Talos service's log) of one
or more nodes, merged onto one stream. Without --follow it prints what the node
has and exits once nothing new arrives for --idle-timeout. With --follow it keeps
streaming until Ctrl-C and, if the connection drops or ichor-ui drains, reconnects
and resumes from the last resume token (a few lines around the cut may repeat or
be missed: Talos offers no per-line identity). Output is plain
text (prefixed with the node name when several are given); -o json prints one
JSON object per line instead.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFormat(outputFormat); err != nil {
			return err
		}

		deps, err := newAPIDeps(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		connectFn := func(ctx context.Context) (*apiClients, error) { return connectAPI(ctx, apiOpts, deps, true) }

		return runNodeLogs(ctx, connectFn, cmd.OutOrStdout(), cmd.ErrOrStderr(), args, nodeLogsOpts)
	},
}

// runNodeLogs drives StreamNodeLogs, reconnecting with the latest resume
// token when --follow is set (the Go twin of ui/src/lib/resumableStream.ts).
func runNodeLogs(
	ctx context.Context, dial func(context.Context) (*apiClients, error), out, errw io.Writer, names []string, opts nodeLogsOptions,
) error {
	req := &pb.StreamNodeLogsRequest{Service: opts.Service, ResumeToken: opts.ResumeToken}
	for _, n := range names {
		req.Machines = append(req.Machines, objectRef(apiOpts.Namespace, n))
	}

	emit := func(m *pb.StreamNodeLogsResponse) {
		if outputFormat == outputJSON {
			raw, _ := protojson.Marshal(m)
			_, _ = fmt.Fprintf(out, "%s\n", raw)

			return
		}

		if len(names) > 1 {
			_, _ = fmt.Fprintf(out, "%s: %s\n", m.GetMachine().GetName(), m.GetMessage())

			return
		}

		_, _ = fmt.Fprintln(out, m.GetMessage())
	}

	for {
		c, err := dial(ctx)
		if err == nil {
			var token string

			token, err = streamLogsOnce(ctx, c, req, emit, idleFor(opts))
			c.Close()

			if token != "" {
				req.ResumeToken = token
			}
		}

		if ctx.Err() != nil {
			return nil
		}

		retryable := err == nil || connect.CodeOf(err) == connect.CodeUnavailable

		if !opts.Follow || !retryable {
			return err
		}

		logsReconnectNotice(errw, err)

		if sleepCtx(ctx, logsReconnectDelay) != nil {
			return nil
		}
	}
}

func idleFor(opts nodeLogsOptions) time.Duration {
	if opts.Follow {
		return 0
	}

	return opts.Idle
}

// streamLogsOnce runs one connection and returns the latest resume token
// seen. idle > 0 ends the stream cleanly after that long without a message.
func streamLogsOnce(
	ctx context.Context, c *apiClients, req *pb.StreamNodeLogsRequest, emit func(*pb.StreamNodeLogsResponse), idle time.Duration,
) (string, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var idleFired atomic.Bool

	var timer *time.Timer

	if idle > 0 {
		timer = time.AfterFunc(idle, func() { idleFired.Store(true); cancel() })
		defer timer.Stop()
	}

	stream, err := c.Node.StreamNodeLogs(sctx, connect.NewRequest(req))
	if err != nil {
		if idleFired.Load() {
			return "", nil
		}

		return "", err
	}

	defer func() { _ = stream.Close() }()

	token := ""

	for stream.Receive() {
		m := stream.Msg()
		if m.GetResumeToken() != "" {
			token = m.GetResumeToken()
		}

		emit(m)

		if timer != nil {
			timer.Reset(idle)
		}
	}

	if err := stream.Err(); err != nil && !idleFired.Load() && !errors.Is(err, context.Canceled) {
		return token, err
	}

	return token, nil
}

func init() {
	rootCmd.AddCommand(nodeCmd)
	addAPIFlags(nodeCmd)
	nodeCmd.AddCommand(nodeGetCmd, nodeLogsCmd)

	f := nodeLogsCmd.Flags()
	f.StringVar(&nodeLogsOpts.Service, "service", "", "Talos service whose log to read (kubelet, etcd, ...); empty means the kernel log")
	f.BoolVarP(&nodeLogsOpts.Follow, "follow", "f", false, "keep streaming, reconnecting with the resume token if the connection drops")
	f.StringVar(&nodeLogsOpts.ResumeToken, "resume-token", "", "resume token from an earlier run (opaque)")
	f.DurationVar(&nodeLogsOpts.Idle, "idle-timeout", 3*time.Second, "without --follow, stop after this long without a new line")
}
