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
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// Human output of the commands that change one thing: one ✓ line that says what
// happened, and under it a ! line for each finding the server raised.

// done prints the one ✓ line of a command that changed something.
func (h *human) done(format string, args ...any) {
	h.Note(h.Mark(markOK) + " " + fmt.Sprintf(format, args...))
}

// warn prints a ! line.
func (h *human) warn(msg string) { h.Note(h.Mark(markWarning) + " " + msg) }

// nsFlag is " -n NAMESPACE" for a command the reader is told to run next, unless the namespace is the
// default of the API commands.
func nsFlag(namespace string) string {
	if namespace == ichorControllerNamespace {
		return ""
	}

	return " -n " + namespace
}

// machineLabel is how a machine is named in a sentence: the first eight characters of its name
// (the UUID it is named after).
func machineLabel(name string) string { return shortNames([]string{name})[0] }

// renderMachineApproved prints "✓ approved fa70962a as control-plane in initshow" to h and the
// admission warnings (advice, not refusals) to warnings.
func renderMachineApproved(h, warnings *human, resp *pb.ApproveMachineResponse) error {
	m := resp.GetMachine()

	line := "approved " + machineLabel(m.GetRef().GetName())
	if role := humanEnum(m.GetRole().String()); role != "" {
		line += " as " + role
	}

	if c := m.GetCluster().GetName(); c != "" {
		line += " in " + c
	}

	h.done("%s", line)

	for _, w := range resp.GetWarnings() {
		warnings.warn(w)
	}

	return cmp.Or(h.Err(), warnings.Err())
}

// renderMachineRejected prints "✓ rejected fa70962a".
func renderMachineRejected(h *human, name string) error {
	h.done("rejected %s", machineLabel(name))

	return h.Err()
}

// renderInspection prints what reclaim found on the node and what the reset would do, as a block,
// then the warnings and refusals of the assessment.
func renderInspection(h *human, in reclaim.Inspection, p reclaim.Plan) {
	role := "worker"
	if in.ControlPlane {
		role = "control plane"
	}

	cluster := orNone(in.ClusterName) + " (id " + orNone(in.ClusterID) + "), "
	if in.ClusterReachable {
		cluster += "reachable from the node"
	} else {
		cluster += "not reachable from the node"
	}

	etcd := ""
	switch {
	case in.EtcdKnown:
		etcd = fmt.Sprintf("%d members", len(in.EtcdMembers))
	case in.ControlPlane && in.EtcdProblem != "":
		etcd = "not running or not answering (" + in.EtcdProblem + ")"
	}

	graceful := "not graceful"
	if p.Graceful {
		graceful = "graceful"
	}

	rows := []kv{
		{"Role", role + " · Talos " + in.Version}, {"UUID", orNone(in.UUID)}, {labelCluster, cluster}, {"etcd", etcd},
		{"Reset", "STATE and EPHEMERAL, then reboot · " + graceful},
	}

	if len(in.Volumes) == 0 {
		rows = append(rows, kv{"Volumes", "no orphaned volumes found"})
	}

	for _, v := range in.Volumes {
		val := fmt.Sprintf("%s on %s (%d bytes), not wiped", v.Name, v.Location, v.Size)
		if v.Encrypted {
			val += "  " + h.Mark(markWarning) + " ENCRYPTED: if its key is locked to STATE (lockToState), the reset wipes the salt that key needs, so the data is unreadable afterwards"
		}

		rows = append(rows, kv{"Volume", val})
	}

	h.Block(inspectionTitle(in), rows...)

	for _, s := range p.Warnings {
		h.warn(s)
	}

	for _, s := range p.Refusals {
		h.Note(h.Mark(markFailed) + " refused: " + s)
	}
}

// inspectionTitle titles the inspection: "w1 · 10.0.0.5".
func inspectionTitle(in reclaim.Inspection) string {
	return strings.Join(nonEmpty(in.Hostname, in.Address), " · ")
}
