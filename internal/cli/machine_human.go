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
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// machineNode is the name the machine has as a Kubernetes node, else its hostname.
func machineNode(m *pb.Machine) string { return cmp.Or(m.GetNodeName(), m.GetHostname()) }

func renderMachineList(h *human, resp *pb.ListMachinesResponse) error {
	ms := resp.GetMachines()
	if len(ms) == 0 {
		h.Note("no machines")

		return h.Err()
	}

	names := make([]string, len(ms))
	clusters := map[string]bool{}

	for i, m := range ms {
		names[i] = m.GetRef().GetName()
		clusters[m.GetCluster().GetName()] = true
	}

	names = shortNames(names)
	withCluster := len(clusters) > 1
	header := []string{"name", "role", "approval", "ready", "endpoint", "node", "age"}

	if withCluster {
		header = slices.Insert(header, 1, "cluster")
	}

	rows := make([][]string, len(ms))

	for i, m := range ms {
		row := []string{names[i], humanEnum(m.GetRole().String()), humanEnum(m.GetApprovalState().String()),
			h.readyMark(m.GetConditions()), m.GetEndpoint(), machineNode(m), humanAge(m.GetCreatedAt())}
		if withCluster {
			row = slices.Insert(row, 1, m.GetCluster().GetName())
		}

		rows[i] = row
	}

	h.Table(header, rows)

	return h.Err()
}

// diskDesc names a disk by the first identifier it has.
func diskDesc(d *pb.MachineDisk) string {
	return cmp.Or(d.GetDevPath(), d.GetByIdPath(), d.GetWwid(), d.GetSerial(), d.GetBusPath())
}

// humanBytes is a size as GiB or TiB.
func humanBytes(n int64) string {
	const gib = 1 << 30

	switch {
	case n <= 0:
		return ""
	case n >= 1024*gib:
		return fmt.Sprintf("%.1f TiB", float64(n)/(1024*gib))
	default:
		return fmt.Sprintf("%.0f GiB", float64(n)/gib)
	}
}

func approvalRow(m *pb.Machine) string {
	s := humanEnum(m.GetApprovalState().String())

	d := m.GetApprovalDecision()
	if d == nil {
		return s
	}

	if d.GetActor() != "" {
		s += " by " + d.GetActor()
	}

	s += agoSuffix(d.GetAt())

	if d.GetReason() != "" {
		s += ": " + d.GetReason()
	}

	return s
}

func renderMachineGet(h *human, m *pb.Machine) error {
	h.Block(nsName(m.GetRef()), kv{"Cluster", m.GetCluster().GetName()}, kv{"Role", humanEnum(m.GetRole().String())},
		kv{"Approval", approvalRow(m)}, kv{"Health", machineHealth(h, m)}, kv{"Node", m.GetNodeName()},
		kv{"Hostname", m.GetHostname()}, kv{"Endpoint", m.GetEndpoint()}, kv{"Stage", m.GetStage()},
		kv{"Deleting", map[bool]string{true: "yes"}[m.GetDeleting()]}, kv{"Created", agoOrEmpty(m.GetCreatedAt())})

	id := m.GetIdentity()
	h.Block("Identity", kv{"UUID", id.GetUuid()}, kv{"Serial", id.GetSerial()}, kv{"MACs", strings.Join(id.GetMacAddresses(), ", ")})

	if in := m.GetInstall(); in != nil {
		h.Block("Install", kv{"Disk", diskDesc(in.GetDisk())}, kv{"Image", in.GetImage()})
	}

	if n := m.GetNetwork(); n != nil {
		vlan := ""
		if n.GetVlanId() != 0 {
			vlan = fmt.Sprint(n.GetVlanId())
		}

		h.Block("Network", kv{"MAC", n.GetMac()}, kv{"Addressing", n.GetAddressing()}, kv{"Addresses", strings.Join(n.GetAddresses(), ", ")},
			kv{"Gateway", n.GetGateway()}, kv{"DNS", strings.Join(n.GetDnsServers(), ", ")}, kv{"VLAN", vlan})
	}

	var disks []kv
	for _, d := range m.GetDisks() {
		disks = append(disks, kv{cmp.Or(d.GetName(), d.GetRole()), diskDesc(d.GetDisk())})
	}

	h.Block("Disks", disks...)

	var labels []kv
	for _, k := range slices.Sorted(maps.Keys(m.GetNodeLabels())) {
		labels = append(labels, kv{k, m.GetNodeLabels()[k]})
	}

	h.Block("Node labels", labels...)
	h.Block("Needs a reboot", kv{"Fields", strings.Join(m.GetRebootPaths(), ", ")})

	var found []kv
	for _, d := range m.GetDiscoveredDisks() {
		found = append(found, kv{diskDesc(&pb.MachineDisk{DevPath: d.GetDevPath(), ByIdPath: d.GetByIdPath(), Wwid: d.GetWwid(), Serial: d.GetSerial()}),
			strings.Join(nonEmpty(humanBytes(d.GetSizeBytes()), d.GetModel(), d.GetTransport()), " · ")})
	}

	h.Block("Discovered disks", found...)

	var links []kv

	for _, l := range m.GetDiscoveredLinks() {
		up := "down"
		if l.GetLinkUp() {
			up = "up"
		}

		speed := ""
		if l.GetSpeedMbit() > 0 {
			speed = fmt.Sprintf("%d Mbit/s", l.GetSpeedMbit())
		}

		links = append(links, kv{l.GetName(), strings.Join(nonEmpty(l.GetPermanentMac(), up, speed, l.GetDriver()), " · ")})
	}

	h.Block("Discovered NICs", links...)
	h.Conditions("Conditions", conditionsOf(m.GetConditions()))

	return h.Err()
}

func nonEmpty(ss ...string) []string {
	return slices.DeleteFunc(slices.Clone(ss), func(s string) bool { return s == "" })
}

// machineHealth is the Ready condition as a phrase; empty for a machine without one (pending).
func machineHealth(h *human, m *pb.Machine) string {
	if h.readyMark(m.GetConditions()) == "" {
		return ""
	}

	return h.health(m.GetConditions())
}
