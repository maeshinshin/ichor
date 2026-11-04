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
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// conditionsOf is conds as the rows of human.Conditions.
func conditionsOf(conds []*pb.Condition) []conditionRow {
	rows := make([]conditionRow, len(conds))
	for i, c := range conds {
		rows[i] = conditionRow{Mark: conditionMark(c.GetType(), c.GetStatus()), Type: c.GetType(), Reason: c.GetReason(), Message: c.GetMessage()}
	}

	return rows
}

// readyMark is the glyph of the Ready condition, "" when there is none yet.
func (h *human) readyMark(conds []*pb.Condition) string {
	for _, c := range conds {
		if c.GetType() == "Ready" {
			return h.Mark(conditionMark("Ready", c.GetStatus()))
		}
	}

	return ""
}

// health is "✓ ready", "✗ not ready (reason)" or "… waiting" from the Ready condition.
func (h *human) health(conds []*pb.Condition) string {
	for _, c := range conds {
		if c.GetType() != "Ready" {
			continue
		}

		switch c.GetStatus() {
		case "True":
			return h.Mark(markOK) + " ready"
		case "False":
			return h.Mark(markFailed) + " not ready" + parenthesized(c.GetReason())
		}
	}

	return h.Mark(markPending) + " waiting"
}

func parenthesized(s string) string {
	if s == "" {
		return ""
	}

	return " (" + s + ")"
}

// cniColumn is the CNI of a cluster for a table: "cilium", or "unmanaged (flannel)".
func cniColumn(cl *pb.Cluster) string {
	if cl.GetCniPreset() != ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return "cilium"
	}

	if d := cl.GetCniDetected(); d != "" {
		return "unmanaged (" + d + ")"
	}

	return "unmanaged"
}

// versionOf is the entry of the release-set component name ("cilium", "talos", ...), nil if none.
func versionOf(cl *pb.Cluster, name string) *pb.ComponentVersion {
	for _, v := range cl.GetVersions() {
		if v.GetName() == name {
			return v
		}
	}

	return nil
}

func nsName(ref *pb.ObjectRef) string { return ref.GetName() + " · " + ref.GetNamespace() }

func renderClusterList(h *human, resp *pb.ListClustersResponse) error {
	cls := resp.GetClusters()
	if len(cls) == 0 {
		h.Note("no clusters")

		return h.Err()
	}

	namespaces := map[string]bool{}
	for _, cl := range cls {
		namespaces[cl.GetRef().GetNamespace()] = true
	}

	withNS := len(namespaces) > 1
	header := []string{"name", "talos", "kubernetes", "cni", "endpoint", "ready", "age"}

	if withNS {
		header = slices.Insert(header, 0, "namespace")
	}

	rows := make([][]string, len(cls))

	for i, cl := range cls {
		row := []string{cl.GetRef().GetName(), cl.GetTalosVersion(), cl.GetKubernetesVersion(), cniColumn(cl),
			cl.GetEndpoint(), h.readyMark(cl.GetConditions()), humanAge(cl.GetCreatedAt())}
		if withNS {
			row = slices.Insert(row, 0, cl.GetRef().GetNamespace())
		}

		rows[i] = row
	}

	h.Table(header, rows)

	return h.Err()
}

func endpointWithType(cl *pb.Cluster) string {
	if cl.GetEndpointType() == "" {
		return cl.GetEndpoint()
	}

	return cl.GetEndpoint() + " (" + cl.GetEndpointType() + ")"
}

func renderClusterStatus(h *human, cl *pb.Cluster) error {
	rows := []kv{{"Talos", cl.GetTalosVersion()}, {"Kubernetes", cl.GetKubernetesVersion()}}

	switch cilium := versionOf(cl, "cilium"); {
	case cl.GetCniPreset() == ichorv1alpha1.TalosClusterCNIPresetUnmanaged:
		rows = append(rows, kv{"CNI", cniColumn(cl) + ", not managed by Ichor"})
	case cilium.GetCurrent() != "":
		rows = append(rows, kv{"Cilium", cilium.GetCurrent()})
	default:
		rows = append(rows, kv{"CNI", "cilium"})
	}

	rows = append(rows, kv{"Endpoint", endpointWithType(cl)}, kv{"Health", h.health(cl.GetConditions())})
	h.Block(nsName(cl.GetRef()), rows...)

	return h.Err()
}

// versionRow is "v1.14.5", or "v1.14.3 (behind: this Ichor ships v1.14.5)" when it differs from the release set.
func versionRow(v *pb.ComponentVersion) string {
	cur := v.GetCurrent()
	if cur == "" {
		cur = "unknown"
	}

	if v.GetState() == "up-to-date" || v.GetTarget() == "" {
		return cur
	}

	return fmt.Sprintf("%s (%s: this Ichor ships %s)", cur, v.GetState(), v.GetTarget())
}

var componentLabels = map[string]string{"talos": "Talos", "kubernetes": "Kubernetes", "cilium": "Cilium", "version-contract": "Contract"}

func date(ts *timestamppb.Timestamp) string { return ts.AsTime().UTC().Format("2006-01-02") }

func renderClusterGet(h *human, cl *pb.Cluster) error {
	h.Block(nsName(cl.GetRef()), kv{"Health", h.health(cl.GetConditions())}, kv{"Created", agoOrEmpty(cl.GetCreatedAt())})

	var vers []kv

	for _, v := range cl.GetVersions() {
		label := componentLabels[v.GetName()]
		if label == "" {
			label = v.GetName()
		}

		vers = append(vers, kv{label, versionRow(v)})
	}

	if len(vers) == 0 { // an older backend sends no comparison
		vers = []kv{{"Talos", cl.GetTalosVersion()}, {"Kubernetes", cl.GetKubernetesVersion()}}
	}

	h.Block("Versions", vers...)
	h.Block("Endpoint", kv{"Address", cl.GetEndpoint()}, kv{"Type", cl.GetEndpointType()})
	h.Block("Network", kv{"Pods", strings.Join(cl.GetPodSubnets(), ", ")}, kv{"Services", strings.Join(cl.GetServiceSubnets(), ", ")})
	h.Block("CNI", kv{"Preset", cl.GetCniPreset()}, kv{"Detected", strings.TrimSpace(cl.GetCniDetected() + " " + cl.GetCniDetectedVersion())},
		kv{"Tunnel", cl.GetCniTunnel()}, kv{"Kube-proxy", cl.GetKubeProxyMode()})
	h.Block("Control plane", kv{"Nodes", fmt.Sprint(cl.GetControlPlaneCount())}, kv{"Schedulable", yesNo(cl.GetControlPlaneSchedulable())})
	h.Block("Discovery", kv{"Addresses", strings.Join(cl.GetDiscoveryAddresses(), ", ")}, kv{"Ranges", strings.Join(cl.GetDiscoveryCidrs(), ", ")},
		kv{"Already running", strings.Join(cl.GetAlreadyConfiguredAddresses(), ", ")})

	if r := cl.GetRollout(); r != nil {
		h.Block("Rollout", kv{"Kind", humanEnum(r.GetKind().String())}, kv{"Target", r.GetTargetVersion()},
			kv{"Progress", fmt.Sprintf("%d/%d nodes", r.GetNodesDone(), r.GetNodesTotal())}, kv{"Phase", r.GetCurrentPhase()},
			kv{"Paused", map[bool]string{true: "yes"}[r.GetPaused()]})
	}

	var handovers []kv
	for _, s := range cl.GetHandovers() {
		handovers = append(handovers, kv{s.GetComponent(), humanEnum(s.GetPhase().String()) + agoSuffix(s.GetLastTransitionTime())})
	}

	h.Block("Hand-over", handovers...)

	var certs []kv
	for _, c := range cl.GetCertificates() {
		certs = append(certs, kv{c.GetName(), date(c.GetNotAfter()) + " (" + humanUntil(c.GetNotAfter()) + ")"})
	}

	h.Block("Certificates", certs...)
	h.Conditions("Conditions", conditionsOf(cl.GetConditions()))

	return h.Err()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}

// agoOrEmpty is "5m ago", "" when ts is unset.
func agoOrEmpty(ts *timestamppb.Timestamp) string {
	if a := humanAge(ts); a != "" {
		return a + " ago"
	}

	return ""
}

func agoSuffix(ts *timestamppb.Timestamp) string {
	if a := agoOrEmpty(ts); a != "" {
		return " (" + a + ")"
	}

	return ""
}
