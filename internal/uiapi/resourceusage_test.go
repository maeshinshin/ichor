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
	"context"
	"errors"
	"math"
	"testing"
	"time"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/resources/perf"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Talos rewrites the perf.CPU resource only every 30 seconds, so two reads a second or so apart
// returned the very same snapshot and the page showed "-" on every node. The fix reads /proc/stat
// live; this test pins the sampler against both kinds of source.
func TestCPUSamplingNeedsALiveSource(t *testing.T) {
	var calls int

	// live: every read has moved on, as /proc/stat does.
	live := func(context.Context) (cpuReading, error) {
		calls++

		return cpuReading{Cores: 4, Total: perf.CPUStat{User: float64(calls), Idle: float64(calls) * 3}}, nil
	}

	usage := &pb.ResourceUsage{}
	sampleCPUUsage(context.Background(), live, time.Millisecond, usage)

	if !usage.CpuSampled || math.Abs(usage.CpuCoresUsed-1) > 1e-9 {
		t.Fatalf("live source: sampled=%v cores=%v, want 1 core", usage.CpuSampled, usage.CpuCoresUsed)
	}

	// cached: the same snapshot every time, like perf.CPU between its 30 s updates.
	cached := func(context.Context) (cpuReading, error) {
		return cpuReading{Cores: 4, Total: perf.CPUStat{User: 10, Idle: 30}}, nil
	}

	usage = &pb.ResourceUsage{}
	sampleCPUUsage(context.Background(), cached, time.Millisecond, usage)

	if usage.CpuSampled {
		t.Fatal("identical samples must stay unsampled")
	}
}

func TestCPUSamplingErrorLeavesTheUsageUnsampled(t *testing.T) {
	fail := func(context.Context) (cpuReading, error) { return cpuReading{}, errors.New("denied") }

	usage := &pb.ResourceUsage{}
	sampleCPUUsage(context.Background(), fail, time.Millisecond, usage)

	if usage.CpuSampled {
		t.Fatal("a failed read must leave the usage unsampled")
	}
}

func TestMemoryIsReadAsKibibytes(t *testing.T) {
	// A 4 GiB node reports 4194304 kB; the page showed that as "4 MB".
	if got := kibToBytes(4194304); got != 4<<30 {
		t.Fatalf("got %d", got)
	}
}

func TestCPUCoresUsedComesFromTheDifferenceOfTwoSamples(t *testing.T) {
	// Cumulative seconds: between the samples 4 cores ran 10s of wall time
	// (40 core-seconds), 10 of them busy.
	prev := perf.CPUStat{User: 1000, System: 500, Idle: 100000}
	next := perf.CPUStat{User: 1006, System: 504, Idle: 100030}

	got, ok := cpuCoresUsed(prev, next, 4)
	if !ok || math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("got %v %v, want 1.0 core (10 busy of 40 core-seconds, over 4 cores)", got, ok)
	}

	if got < 0 {
		t.Fatalf("a usage can never be negative: %v", got)
	}

	if _, ok := cpuCoresUsed(next, next, 4); ok {
		t.Fatal("counters that did not move say nothing")
	}

	if _, ok := cpuCoresUsed(next, prev, 4); ok {
		t.Fatal("counters that went backwards are rejected")
	}
}

func TestDataFilesystemPrefersVarThenTheLargestMount(t *testing.T) {
	stats := []*machineapi.MountStat{
		{MountedOn: "/", Size: 1000, Available: 0},
		{MountedOn: "/var", Size: 200, Available: 150},
	}

	if used, total := dataFilesystem(stats); used != 50 || total != 200 {
		t.Fatalf("got %d/%d", used, total)
	}

	if used, total := dataFilesystem(stats[:1]); used != 1000 || total != 1000 {
		t.Fatalf("fallback got %d/%d", used, total)
	}

	if used, total := dataFilesystem(nil); used != 0 || total != 0 {
		t.Fatalf("none got %d/%d", used, total)
	}
}
