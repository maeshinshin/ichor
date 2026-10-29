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

package etcdbackup

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Talos's defaults for a cluster that did not declare spec.network (api/v1alpha1 TalosClusterNetwork).
var defaultClusterCIDRs = []string{"10.244.0.0/16", "10.96.0.0/12"}

// InClusterReason says why the backup store at endpoint lives inside cluster, or "" when it does not
// (or cannot be told). A restore wipes etcd, so the Kubernetes API, and with it a Service-backed store,
// is gone while the restore marker is still to be written to the store: a store inside the cluster
// being restored deadlocks the restore. In cluster means a host of the form *.svc or
// *.svc.cluster.local, or an IP inside the cluster's service or pod CIDRs.
func InClusterReason(endpoint string, cluster *ichorv1alpha1.TalosCluster) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return ""
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")

	if strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local") {
		return fmt.Sprintf("the endpoint host %q is a Kubernetes Service name", host)
	}

	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}

	pods, services := defaultClusterCIDRs[:1], defaultClusterCIDRs[1:]
	if n := cluster.Spec.Network; n != nil {
		if len(n.PodSubnets) > 0 {
			pods = n.PodSubnets
		}

		if len(n.ServiceSubnets) > 0 {
			services = n.ServiceSubnets
		}
	}

	cidrs := append(append([]string{}, pods...), services...)

	for _, c := range cidrs {
		if _, ipnet, err := net.ParseCIDR(c); err == nil && ipnet.Contains(ip.AsSlice()) {
			return fmt.Sprintf("the endpoint address %s is inside the cluster's pod or service range %s", ip, c)
		}
	}

	return ""
}

// InClusterMessage is the sentence the restore plan and the TalosEtcdBackup condition give.
func InClusterMessage(reason string) string {
	return reason + ": the backup store must live outside the cluster being restored, because a restore takes the " +
		"Kubernetes API down and the store with it, and the restore cannot finish without the store"
}
