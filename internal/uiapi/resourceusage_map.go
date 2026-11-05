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

package uiapi

import (
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/resources/perf"
)

// dataMountPoint is Talos's EPHEMERAL volume, the filesystem that holds
// container images, etcd and logs: the one an operator means by "disk full".
const dataMountPoint = "/var"

// kibToBytes converts perf.Memory's values, which are kibibytes as in
// /proc/meminfo, to bytes.
func kibToBytes(kib uint64) int64 {
	return int64(kib) * 1024 //nolint:gosec // memory sizes fit in int64
}

// cpuBusyAndTotal splits a cumulative perf.CPU sample (seconds per state) into
// time spent busy and total time. Idle and iowait count as not busy.
func cpuBusyAndTotal(s perf.CPUStat) (busy, total float64) {
	idle := s.Idle + s.Iowait
	total = s.User + s.Nice + s.System + s.Idle + s.Iowait + s.Irq + s.SoftIrq + s.Steal

	return total - idle, total
}

// cpuCoresUsed turns two cumulative samples of the all-CPU total into the
// number of cores kept busy between them. ok is false when no time has passed
// between the samples (the counters have not moved yet) or the counters went
// backwards.
func cpuCoresUsed(prev, next perf.CPUStat, cores int) (float64, bool) {
	b0, t0 := cpuBusyAndTotal(prev)
	b1, t1 := cpuBusyAndTotal(next)

	dTotal := t1 - t0
	if dTotal <= 0 || b1 < b0 {
		return 0, false
	}

	return min((b1-b0)/dTotal, 1) * float64(cores), true
}

// dataFilesystem picks the used and total bytes of the data filesystem from the
// Mounts API: /var when mounted, otherwise the largest mount.
func dataFilesystem(stats []*machineapi.MountStat) (used, total int64) {
	var pick *machineapi.MountStat

	for _, s := range stats {
		if s.GetMountedOn() == dataMountPoint {
			pick = s

			break
		}

		if pick == nil || s.GetSize() > pick.GetSize() {
			pick = s
		}
	}

	if pick == nil || pick.GetSize() < pick.GetAvailable() {
		return 0, 0
	}

	return int64(pick.GetSize() - pick.GetAvailable()), int64(pick.GetSize()) //nolint:gosec // filesystem sizes fit in int64
}
