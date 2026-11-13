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
	"net"
	"net/url"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"k8s.io/client-go/rest"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// A live restore wipes the control plane the kubeconfig usually points at, and the apiserver there is
// unreachable for many minutes; the other control planes' apiservers answer. restore etcd --follow
// therefore learns the control planes' addresses up front and, while the current server is
// unreachable, tries the others in turn.

// kubeAPIPort is the apiserver port assumed for a control plane when the kubeconfig's server names none.
const kubeAPIPort = "6443"

// serverURL is host (an IP or name, no port) as an apiserver URL with the port and scheme of base.
func serverURL(base, host string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "https://" + net.JoinHostPort(host, kubeAPIPort)
	}

	port := u.Port()
	if port == "" {
		port = kubeAPIPort
	}

	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}

	return scheme + "://" + net.JoinHostPort(host, port)
}

// otherServer is cfg pointed at server, still verifying the certificate against the name of the
// original server (unless the kubeconfig set its own).
func otherServer(cfg *rest.Config, server string) *rest.Config {
	c := rest.CopyConfig(cfg)

	if c.ServerName == "" {
		if u, err := url.Parse(cfg.Host); err == nil && u.Hostname() != "" {
			c.ServerName = u.Hostname()
		} else {
			c.ServerName = strings.Split(cfg.Host, ":")[0]
		}
	}

	c.Host = server

	return c
}

// controlPlaneServers lists the apiserver URLs of the cluster's control planes, from the machines'
// Talos endpoints, without the one already in use.
func controlPlaneServers(machines []*pb.Machine, current string) []string {
	var out []string

	for _, m := range machines {
		if m.GetRole() != pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE || m.GetDeleting() || m.GetEndpoint() == "" {
			continue
		}

		host := m.GetEndpoint()
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		s := serverURL(current, host)
		if hostOf(s) == hostOf(current) || slices.Contains(out, s) {
			continue
		}

		out = append(out, s)
	}

	slices.Sort(out)

	return out
}

func hostOf(server string) string {
	if u, err := url.Parse(server); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}

	return server
}

// enableFailover arms c to move to another control plane's apiserver while its own is unreachable. It
// does nothing for clients that cannot dial another server (the ichor-ui tunnel has its own reconnect).
func enableFailover(ctx context.Context, c *apiClients, errw io.Writer, cluster *pb.ObjectRef) {
	if c.Dial == nil || c.Server == "" {
		return
	}

	resp, err := c.Machine.ListMachines(ctx, connect.NewRequest(&pb.ListMachinesRequest{Cluster: cluster}))
	if err != nil {
		_, _ = fmt.Fprintf(errw, "ichor: no failover to other API servers (reading the nodes: %v); --follow will retry %s\n", err, c.Server)

		return // best effort: the follow still retries the one server
	}

	ring := append([]string{c.Server}, controlPlaneServers(resp.Msg.GetMachines(), c.Server)...)
	if len(ring) < 2 {
		_, _ = fmt.Fprintf(errw, "ichor: no failover to other API servers (no other control plane with an endpoint is known); --follow will retry %s\n", c.Server)

		return
	}

	_, _ = fmt.Fprintf(errw, "ichor: API servers for --follow, in order of failover: %s\n", strings.Join(ring, ", "))

	at := 0

	c.Failover = func(ctx context.Context) (string, *apiClients, error) {
		for range len(ring) {
			at = (at + 1) % len(ring)

			nc, err := c.Dial(ctx, ring[at])
			if err == nil {
				return ring[at], nc, nil
			}
		}

		return "", nil, fmt.Errorf("no other control plane to try")
	}
}
