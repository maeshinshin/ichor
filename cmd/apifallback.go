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

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// After an etcd restore the nodes that were not wiped keep a kube-proxy (or CNI) that still holds the old
// cluster's Services, so the kubernetes Service address refuses connections from a pod on such a node,
// including the controller's: the controller is the one that replaces those pods, and so it
// cannot start. The control planes' own addresses do not depend on that state. The controller therefore
// keeps the addresses of the control planes it last saw in a file next to the CA trust file (an emptyDir:
// it survives a container restart, which is what a failed start is) and, when the Service address has
// not answered for apiFallbackAfter, tries those too, one after the other with the Service address
// between them. It stays on the address that answered until the next start.
const (
	apiFallbackAfter = 30 * time.Second
	apiServersEvery  = 5 * time.Minute
	apiServerPort    = "6443"
	// apiServerName is a name every kube-apiserver certificate of Talos carries, so that dialling a control
	// plane by address verifies the same certificate the Service address does.
	apiServerName = "kubernetes.default.svc"
	controlPlane  = "node-role.kubernetes.io/control-plane"
)

// apiServersFile is the file of control plane addresses, one per line.
func apiServersFile(trustFile string) string {
	return filepath.Join(filepath.Dir(trustFile), "apiservers")
}

func readAPIServers(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var out []string

	for l := range strings.FieldsSeq(string(data)) {
		if net.ParseIP(l) != nil {
			out = append(out, l)
		}
	}

	return out
}

func writeAPIServers(path string, addrs []string) error {
	return os.WriteFile(path, []byte(strings.Join(addrs, "\n")+"\n"), 0o600)
}

// withAPIFallback wraps get so that, once start is more than after in the past, every other call (the first one first) returns the
// config pointed at the next address instead of the first one.
func withAPIFallback(get func() (*rest.Config, error), addrs func() []string, after time.Duration, now func() time.Time) func() (*rest.Config, error) {
	var (
		start time.Time
		late  int // calls since the delay passed
	)

	return func() (*rest.Config, error) {
		cfg, err := get()
		if err != nil {
			return nil, err
		}

		if start.IsZero() {
			start = now()
		}

		list := addrs()
		if now().Sub(start) < after || len(list) == 0 {
			return cfg, nil
		}

		late++
		if late%2 == 0 {
			return cfg, nil
		}

		next := rest.CopyConfig(cfg)
		next.Host = "https://" + net.JoinHostPort(list[(late/2)%len(list)], apiServerPort)
		next.ServerName = apiServerName

		return next, nil
	}
}

// apiServerRecorder keeps the file current. It needs no leader election: every replica has its own file.
type apiServerRecorder struct {
	reader client.Reader
	path   string
}

func (r *apiServerRecorder) NeedLeaderElection() bool { return false }

func (r *apiServerRecorder) Start(ctx context.Context) error {
	tick := time.NewTicker(apiServersEvery)
	defer tick.Stop()

	for {
		r.record(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func (r *apiServerRecorder) record(ctx context.Context) {
	var nodes corev1.NodeList
	if err := r.reader.List(ctx, &nodes, client.HasLabels{controlPlane}); err != nil {
		return // the API is not answering: keep what the file has
	}

	var addrs []string

	for i := range nodes.Items {
		for _, a := range nodes.Items[i].Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				addrs = append(addrs, a.Address)
			}
		}
	}

	slices.Sort(addrs)

	if len(addrs) == 0 || slices.Equal(addrs, readAPIServers(r.path)) {
		return
	}

	if err := writeAPIServers(r.path, addrs); err != nil {
		logf.FromContext(ctx).Error(err, "could not keep the control plane addresses for a start while the Service address is down")
	}
}
