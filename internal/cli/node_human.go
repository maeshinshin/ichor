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
	"io"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Human output of the node commands. `logs node` streams raw lines and stays as it
// is; only its status line is in the new style.

// gibPrecise is a size as MiB below a GiB and GiB or TiB above, one decimal for the larger ones.
func gibPrecise(n int64) string {
	const (
		mib = 1 << 20
		gib = 1 << 30
	)

	switch {
	case n <= 0:
		return ""
	case n < gib:
		return fmt.Sprintf("%d MiB", n/mib)
	case n >= 1024*gib:
		return fmt.Sprintf("%.1f TiB", float64(n)/(1024*gib))
	default:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	}
}

// usage is "used / total", with the unit once; used may be unknown.
func usage(used, total int64) string {
	if total <= 0 {
		return ""
	}

	return gibPrecise(used) + " / " + gibPrecise(total)
}

// renderNodeLive prints what a live read of the node adds to its record; describe node prints it after the record.
func renderNodeLive(h *human, n *pb.NodeDetail) error {
	hw := n.GetHardware()
	ru := n.GetResourceUsage()

	cpu := ""
	if hw.GetCpuCores() > 0 {
		cpu = fmt.Sprintf("%d cores", hw.GetCpuCores())
		if ru.GetCpuSampled() {
			cpu += fmt.Sprintf(" (%.2f in use)", ru.GetCpuCoresUsed())
		}
	}

	h.Block("Hardware", kv{"Manufacturer", hw.GetManufacturer()}, kv{"Model", hw.GetModel()},
		kv{"Serial", hw.GetSerialNumber()}, kv{"CPU", cpu},
		kv{"Memory", usage(ru.GetMemoryUsedBytes(), cmp.Or(ru.GetMemoryTotalBytes(), hw.GetMemoryTotalBytes()))},
		kv{"Filesystem", usage(ru.GetFilesystemUsedBytes(), ru.GetFilesystemTotalBytes())})

	switch k := n.GetKubernetes(); {
	case k != nil:
		ready := h.Mark(markOK) + " ready"
		if !k.GetReady() {
			ready = h.Mark(markFailed) + " not ready"
		}

		h.Block("Kubernetes", kv{labelNode, k.GetNodeName()}, kv{"Ready", ready}, kv{"Kubelet", k.GetKubeletVersion()},
			kv{"Schedulable", yesNo(k.GetSchedulable())})
	case n.GetKubernetesPermissionDenied():
		h.Block("Kubernetes", kv{labelNode, "you may not read the Kubernetes Node"})
	}

	if len(n.GetDisks()) > 0 {
		rows := make([][]string, len(n.GetDisks()))

		for i, d := range n.GetDisks() {
			rows[i] = []string{cmp.Or(d.GetDevPath(), d.GetByIdPath(), d.GetWwid(), d.GetSerial(), d.GetBusPath()),
				humanEnum(d.GetRole().String()), gibPrecise(d.GetSizeBytes()), cmp.Or(d.GetWwid(), d.GetSerial())}
		}

		h.Title("Live disks")
		h.Table([]string{"disk", colRole, "size", "id"}, rows)
	}

	return h.Err()
}

// renderMachineRemoved is the result of `delete node`: the removal started.
func renderMachineRemoved(name string) func(*human, *pb.RemoveMachineResponse) error {
	return func(h *human, _ *pb.RemoveMachineResponse) error {
		h.Note(h.Mark(markOK) + " removing node " + name + ": the controller drains it, removes its etcd member (control plane), wipes it and deletes its Kubernetes Node")

		return h.Err()
	}
}

// nodeRemoveNext tells what a remove did and how to bring the machine back.
func nodeRemoveNext(errw io.Writer, reboot bool, wipeMode string) {
	wiped := "its STATE and EPHEMERAL partitions"

	switch wipeMode {
	case ichorv1alpha1.WipeModeSystemDisk:
		wiped = "its whole system disk"
	case ichorv1alpha1.WipeModeAll:
		wiped = "its whole system disk"
	case ichorv1alpha1.WipeModeSystemDiskAndUserDisks:
		wiped = "its whole system disk and every user disk"
	}

	did := "The controller drains the node, removes its etcd member (control plane), wipes " + wiped + ", powers it off and deletes its Kubernetes Node."
	back := "to add it again: power it on; it boots into maintenance mode and shows up as a new pending node, which you can approve"

	if wipeMode != "" {
		back = "to add it again: power it on; it starts from its boot image and shows up as a new pending node, which you can approve"
	}

	if reboot {
		rebootInto := " into maintenance mode"
		if wipeMode != "" {
			rebootInto = " into its boot image"
		}

		did = "The controller drains the node, removes its etcd member (control plane), wipes " + wiped + ", reboots it" + rebootInto + " and deletes its Kubernetes Node."
		back = "to add it again: it shows up as a new pending node, which you can approve"
	}

	if outputFormat != outputHuman {
		_, _ = fmt.Fprintf(errw, "Next: %s To add it again: %s.\n", did, back)

		return
	}

	p := newProgress(errw)
	p.Info(did)
	p.Next(back)
}

// logsReconnectNotice says the log stream ended and --follow reconnects.
func logsReconnectNotice(errw io.Writer, err error) {
	if outputFormat != outputHuman {
		_, _ = fmt.Fprintf(errw, "ichor: log stream ended (%v); reconnecting...\n", err)

		return
	}

	h := newHuman(errw)
	h.Note(h.Mark(markWarning) + fmt.Sprintf(" log stream ended (%v); reconnecting…", err))
}
