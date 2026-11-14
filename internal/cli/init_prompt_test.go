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
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// setTerminal replaces the stdin terminal check for one test.
func setTerminal(t *testing.T, on bool) {
	t.Helper()

	old := bundleTerminal
	bundleTerminal = func() bool { return on }

	t.Cleanup(func() { bundleTerminal = old })
}

// typed is a terminal that has had keys typed on it and stays open, as a real one does: its reader
// gives the keys, then waits.
func typed(t *testing.T, keys string) io.Reader {
	t.Helper()

	r, w := io.Pipe()

	go func() { _, _ = w.Write([]byte(keys)) }()

	t.Cleanup(func() { _ = r.Close() })

	return r
}

var allButTheDisk = []string{"--cluster", "c", "--endpoint-address", "a", "--node-endpoint", "n:1"}

func newInitFlags(t *testing.T) *pflag.FlagSet {
	t.Helper()

	var (
		o initOptions
		d = initWaitTimeout
	)

	flags := pflag.NewFlagSet("init", pflag.ContinueOnError)
	bindInitFlags(flags, &o, &d)

	return flags
}

// The wording of the required-flag errors is what the e2e harness and CI see when stdin is not a terminal.
func TestInitRequired_NoTerminalKeepsTheErrors(t *testing.T) {
	_, _, err := parsedInit(t, "")

	const want = `required flag(s) "cluster" (file key cluster.name), "endpoint-address" (file key cluster.endpoint.address), ` +
		`"node-endpoint" (file key firstNode.endpoint), "install-disk" (file key firstNode.installDisk) not set`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}

	// With every flag but the disk, the disk is still named.
	_, _, err = parsedInit(t, "", allButTheDisk...)
	if err == nil || !strings.Contains(err.Error(), `"install-disk"`) {
		t.Errorf("install-disk not required: %v", err)
	}
}

func TestInitRequired_NoTerminalEmptyInstallDiskStillRefusedByRunInit(t *testing.T) {
	setTerminal(t, false)
	withTempHome(t)

	deps, _, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())
	deps.ReadNode = func(context.Context, string, bool) (nodeReading, error) {
		t.Error("the node must not be dialed")

		return nodeReading{}, nil
	}

	opts := validInitOptions()
	opts.InstallDisk = ""

	err := runInit(context.Background(), opts, deps)
	if err == nil || err.Error() != "init: --install-disk is required" {
		t.Errorf("error = %v", err)
	}
}

func TestInitRequired_MalformedInstallDiskFailsBeforeTheNode(t *testing.T) {
	_, _, err := parsedInit(t, "", append(allButTheDisk, "--install-disk", "sda")...)
	if err == nil || !strings.HasPrefix(err.Error(), `init: --install-disk "sda": want `) {
		t.Errorf("error = %v", err)
	}
}

func TestInitRequired_TerminalDoesNotRequireTheDisk(t *testing.T) {
	setTerminal(t, true)

	flags := newInitFlags(t)
	if err := flags.Parse(allButTheDisk); err != nil {
		t.Fatal(err)
	}

	if err := checkInitRequired(flags); err != nil {
		t.Errorf("a terminal can be asked for the disk later: %v", err)
	}
}

func TestPromptInitMissing_AsksForEverythingAndSetsTheFlags(t *testing.T) {
	setTerminal(t, true)

	flags := newInitFlags(t)

	var out bytes.Buffer

	// An invalid cluster name is kept for editing (Ctrl-U clears it); a bare Enter takes the default type. The
	// first node's field starts with the workstation's network, so Ctrl-U clears it before the address.
	in := typed(t, "Not_Valid\r\x15demo\r\r192.168.1.9\r\x15192.168.1.50:50000\r")
	if err := promptInitMissing(context.Background(), flags, in, &out); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		flagCluster: tuiCluster, flagEndpointType: endpointVIP, flagEndpointAddress: "192.168.1.9", flagNodeEndpoint: "192.168.1.50:50000",
	} {
		if got, _ := flags.GetString(name); got != want {
			t.Errorf("--%s = %q, want %q", name, got, want)
		}
	}

	setTerminal(t, false)

	if err := checkInitRequired(flags); err == nil || strings.Contains(err.Error(), `"cluster"`) {
		t.Errorf("only the disk should be missing without a terminal: %v", err)
	}
}

func TestPromptInitMissing_HostnameTypeAsksForAHostname(t *testing.T) {
	setTerminal(t, true)

	flags := newInitFlags(t)

	var out bytes.Buffer

	// One row down from vip is loadBalancer; the cluster and node endpoint are given.
	if err := flags.Parse([]string{"--cluster", "c", "--node-endpoint", "n:1"}); err != nil {
		t.Fatal(err)
	}

	if err := promptInitMissing(context.Background(), flags, typed(t, "\x1b[B\rlb.example.com\r"), &out); err != nil {
		t.Fatal(err)
	}

	if got, _ := flags.GetString(flagEndpointType); got != tuiLB {
		t.Errorf("type = %q", got)
	}

	if got, _ := flags.GetString(flagEndpointAddress); got != "lb.example.com" {
		t.Errorf("address = %q", got)
	}
}

func TestPromptInitMissing_AsksOnlyForWhatIsMissing(t *testing.T) {
	flags := newInitFlags(t)
	if err := flags.Parse(allButTheDisk); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := promptInitMissing(context.Background(), flags, strings.NewReader(""), &out); err != nil || out.Len() != 0 {
		t.Errorf("err = %v, output %q", err, out.String())
	}
}

func TestDiskFlagValue(t *testing.T) {
	full := ichorv1alpha1.TalosMachineDiscoveredDisk{
		WWID: "naa.t1", Serial: "St1", ByIDPath: "/dev/disk/by-id/t-x", DevPath: testInstallDisk,
	}

	noWWID := full
	noWWID.WWID = ""

	noSerial := noWWID
	noSerial.Serial = ""

	devOnly := ichorv1alpha1.TalosMachineDiscoveredDisk{DevPath: "/dev/vdt"}

	for _, tc := range []struct {
		d    ichorv1alpha1.TalosMachineDiscoveredDisk
		want string
	}{
		{full, "wwid:naa.t1"}, {noWWID, "serial:St1"}, {noSerial, "/dev/disk/by-id/t-x"}, {devOnly, "/dev/vdt"},
	} {
		got := diskFlagValue(tc.d)
		if got != tc.want {
			t.Errorf("diskFlagValue(%+v) = %q, want %q", tc.d, got, tc.want)
		}

		if _, err := parseDiskSelector("--install-disk", got); err != nil {
			t.Errorf("%q is not accepted by the flag's own parser: %v", got, err)
		}
	}
}

func TestChooseInstallDisk(t *testing.T) {
	a := ichorv1alpha1.TalosMachineDiscoveredDisk{WWID: "w-a", DevPath: testInstallDisk, Model: "M", SizeBytes: 1 << 30, Transport: tuiSata}
	b := ichorv1alpha1.TalosMachineDiscoveredDisk{DevPath: tuiSdb, Serial: "S-b"}

	t.Run("a sole disk is taken and named", func(t *testing.T) {
		var out bytes.Buffer

		got, err := chooseInstallDisk(strings.NewReader(""), &out, []ichorv1alpha1.TalosMachineDiscoveredDisk{a}, nil)
		if err != nil || got != "wwid:w-a" {
			t.Fatalf("got %q, %v", got, err)
		}

		if !strings.Contains(out.String(), "--install-disk wwid:w-a") {
			t.Errorf("the choice was not shown:\n%s", out.String())
		}
	})

	t.Run("no disk is an error naming the flag", func(t *testing.T) {
		_, err := chooseInstallDisk(strings.NewReader(""), &bytes.Buffer{}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "no installable disk") || !strings.Contains(err.Error(), "--install-disk") {
			t.Errorf("error = %v", err)
		}
	})

	t.Run("several disks are a list, with nothing selected to begin with", func(t *testing.T) {
		setTerminal(t, true)

		// Two rows down from the empty first row is the second disk.
		got, err := chooseInstallDisk(typed(t, "\x1b[B\x1b[B\r"), &bytes.Buffer{}, []ichorv1alpha1.TalosMachineDiscoveredDisk{a, b}, nil)
		if err != nil || got != tuiSerialSel {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("a failed inventory falls back to text, keeping an invalid value for editing", func(t *testing.T) {
		setTerminal(t, true)

		var out bytes.Buffer

		got, err := chooseInstallDisk(typed(t, "sda\r\x15/dev/sda\r"), &out, nil, errors.New("rejected"))
		if err != nil || got != testInstallDisk {
			t.Fatalf("got %q, %v", got, err)
		}

		if !strings.Contains(out.String(), "Could not list the node's disks: rejected") {
			t.Errorf("output:\n%s", out.String())
		}
	})
}

// Cancelling at the disk prompt happens before the secrets bundle is written: nothing is left behind.
func TestRunInit_CancelAtTheDiskPromptLeavesNothing(t *testing.T) {
	setTerminal(t, true)

	home := withTempHome(t)
	deps, maint, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())

	var asked bool

	inner := deps.ReadNode
	deps.ReadNode = func(ctx context.Context, e string, want bool) (nodeReading, error) {
		asked = want
		r, err := inner(ctx, e, want)
		r.Disks = []ichorv1alpha1.TalosMachineDiscoveredDisk{{DevPath: testInstallDisk}, {DevPath: tuiSdb}}

		return r, err
	}
	deps.Stdin = strings.NewReader("") // Ctrl-D

	opts := validInitOptions()
	opts.InstallDisk = ""

	if err := runInit(context.Background(), opts, deps); err == nil {
		t.Fatal("expected the cancelled prompt to fail the run")
	}

	if !asked {
		t.Error("the disks were not requested from the node")
	}

	if _, err := os.Stat(filepath.Join(home, ".ichor")); !os.IsNotExist(err) {
		t.Errorf("something was written under ~/.ichor: %v", err)
	}

	if maint.callCount != 0 {
		t.Error("a configuration was applied")
	}
}
