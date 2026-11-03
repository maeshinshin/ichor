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

package cilium

import "github.com/maeshinshin/ichor/pkg/cni"

// Chart coordinates for the upstream Cilium Helm chart. The bootstrap renders
// the copy embedded at chart/cilium-<ChartVersion>.tgz (render.go); Argo CD
// pulls the same version from the repo.
const (
	ChartRepoURL = "https://helm.cilium.io/"
	ChartName    = "cilium"

	// ChartVersion is the Cilium release the values below were written
	// against. Bump it alongside the values when moving to a newer Cilium.
	ChartVersion = "1.20.4"
)

// kubePrismPort is KubePrism's local, load-balanced Kubernetes API port,
// which lets a kube-proxy-free Cilium reach the API server without the
// external endpoint or kube-proxy's own routing
// (docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium, Talos v1.14
// docs, retrieved 2026-10-25: "KubePrism is enabled and configured on port
// 7445").
const kubePrismPort = 7445

// securityContextCapabilities is cilium.securityContext.capabilities: the
// Linux capabilities Cilium needs instead of running privileged
// (docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium, Talos v1.14
// docs / Cilium 1.20.4 chart, checked 2026-11-03). The 1.20.4 chart's own default list
// also adds SYSLOG (dmesg and kernel pointers, not needed by the datapath); Ichor keeps the guide's
// list, because a helm list replaces the chart's rather than merging with it.
type securityContextCapabilities struct {
	CiliumAgent      []string `json:"ciliumAgent"`
	CleanCiliumState []string `json:"cleanCiliumState"`
}

type securityContext struct {
	Capabilities securityContextCapabilities `json:"capabilities"`
}

// cgroup is cilium.cgroup: Talos already mounts the cgroup2 hierarchy
// itself, so Cilium must not try to mount its own
// (docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium, Talos v1.14
// docs / Cilium 1.20.4 chart, checked 2026-11-03).
type cgroup struct {
	AutoMount cgroupAutoMount `json:"autoMount"`
	HostRoot  string          `json:"hostRoot"`
}

type cgroupAutoMount struct {
	Enabled bool `json:"enabled"`
}

type ipam struct {
	Mode string `json:"mode"`
}

// hubbleTLSAuto is cilium.hubble.tls.auto: "cronJob" instead of the
// "helm" default so that Hubble's mTLS certificates are generated and
// rotated by a Kubernetes CronJob rather than a Helm hook -- the hook
// re-runs, and so re-diffs, on every Argo CD sync otherwise
// (docs.cilium.io Helm reference, retrieved 2026-10-25).
type hubbleTLSAuto struct {
	Enabled bool   `json:"enabled"`
	Method  string `json:"method"`
}

type hubbleTLS struct {
	Auto hubbleTLSAuto `json:"auto"`
}

type hubble struct {
	Enabled bool      `json:"enabled"`
	TLS     hubbleTLS `json:"tls"`
}

// certgen is cilium.certgen: the hubble-generate-certs Job. The chart gives it no tolerations, so on
// tainted control planes it stays Pending until a worker exists, the agents wait for its secrets and
// the hand-over never completes. Tolerating the control-plane taint restricts nothing; it only lets
// Ichor's own Cilium components start on the cluster's first nodes.
type certgen struct {
	Tolerations []toleration `json:"tolerations"`
}

type toleration struct {
	Key      string `json:"key"`
	Operator string `json:"operator"`
	Effect   string `json:"effect"`
}

// Values is cilium's Helm values, built from Talos's recommended settings
// (docs.siderolabs.com/kubernetes-guides/cni/deploying-cilium, Talos v1.14
// docs / Cilium 1.20.4 chart, checked 2026-11-03) plus the Hubble CronJob
// setting above. Field order matches the source doc's two values.yaml
// blocks (kube-proxy kept, then kube-proxy replaced) so a diff against it
// stays easy to read.
type Values struct {
	IPAM                 ipam            `json:"ipam"`
	KubeProxyReplacement bool            `json:"kubeProxyReplacement"`
	K8sServiceHost       string          `json:"k8sServiceHost,omitempty"`
	K8sServicePort       int             `json:"k8sServicePort,omitempty"`
	SecurityContext      securityContext `json:"securityContext"`
	Cgroup               cgroup          `json:"cgroup"`
	Hubble               hubble          `json:"hubble"`
	TunnelProtocol       string          `json:"tunnelProtocol,omitempty"`
	Certgen              certgen         `json:"certgen"`
	// Operator is omitted for more than one control plane: the chart's default replica count applies.
	Operator *operator `json:"operator,omitempty"`
}

// operator is cilium.operator: the Cilium operator's replica count. The chart defaults to two, with a
// required pod anti-affinity per node, so on a one-node cluster the second replica never schedules and
// the chart's rolling update, and with it the hand-over, cannot finish. A single
// control plane runs one; the operator's work is control-plane work (IPAM allocation, garbage
// collection), and the datapath keeps running while it restarts.
type operator struct {
	Replicas int `json:"replicas"`
}

// RenderKubernetesVersion is the Kubernetes release the bootstrap render targets. The chart gates some
// templates on the cluster's Kubernetes version (hostUsers from 1.33), and the hand-over renders on the
// real cluster, so the bootstrap must render for the same version or the two differ. It is pkg/release's
// Pinned.Kubernetes; release_test checks that the two agree.
const RenderKubernetesVersion = "v1.36.6"

// valuesFor builds the Helm values for kubeProxy. Both modes share the
// capabilities and cgroup settings Talos needs regardless of kube-proxy;
// only the kube-proxy-replacement block changes.
func (p Preset) valuesFor(kubeProxy cni.KubeProxyMode) Values {
	v := Values{
		TunnelProtocol: p.tunnel,
		IPAM:           ipam{Mode: "kubernetes"},
		SecurityContext: securityContext{Capabilities: securityContextCapabilities{
			CiliumAgent: []string{
				"CHOWN", "KILL", "NET_ADMIN", "NET_RAW", "IPC_LOCK", "SYS_ADMIN",
				"SYS_RESOURCE", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID",
			},
			CleanCiliumState: []string{"NET_ADMIN", "SYS_ADMIN", "SYS_RESOURCE"},
		}},
		Cgroup: cgroup{
			AutoMount: cgroupAutoMount{Enabled: false},
			HostRoot:  "/sys/fs/cgroup",
		},
		Certgen: certgen{Tolerations: []toleration{
			{Key: "node-role.kubernetes.io/control-plane", Operator: "Exists", Effect: "NoSchedule"},
		}},
		Hubble: hubble{
			Enabled: true,
			TLS:     hubbleTLS{Auto: hubbleTLSAuto{Enabled: true, Method: "cronJob"}},
		},
	}

	if kubeProxy == cni.KubeProxyReplaced {
		v.KubeProxyReplacement = true
		v.K8sServiceHost = "localhost"
		v.K8sServicePort = kubePrismPort
	}

	if p.controlPlanes == 1 {
		v.Operator = &operator{Replicas: 1}
	}

	return v
}
