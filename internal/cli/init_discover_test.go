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
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/release"
)

// Literals the tests repeat, as constants (goconst). testHWLink is the link name the package's other tests use.
const (
	testMAC       = "aa:bb:cc:00:00:01"
	testScanRange = "192.168.0.0/24"
	testAddr7     = "192.168.5.7"
	testAddr8     = "192.168.5.8"
	testEndpoint7 = "192.168.5.7:50000"
	testEndpoint8 = "192.168.5.8:50000"
	testFirstNode = "192.168.0.201"
	testWideNet   = "10.0.0.0/22"
	testHostTop   = "10.0.0.8"

	testConfiguredRow = "192.168.5.8  already configured"
)

// The network every test scans is a documentation range: nothing here dials a real address. The fakes stand for
// the connections (dialFunc) and for the reads of the nodes that answer (nodeSearch.ReadNode).
const testNet = "192.168.5.0/24"

// acceptOnly is a network where the addresses in open accept and every other one refuses at once.
func acceptOnly(open ...string) dialFunc {
	return func(_ context.Context, addr string) error {
		if slices.Contains(open, addr) {
			return nil
		}

		return errors.New("connection refused")
	}
}

func fakeNode(host string, configured bool) (nodeReading, error) {
	if configured {
		return nodeReading{}, controller.ErrAlreadyConfigured
	}

	return nodeReading{
		Identity:     controller.HardwareIdentity{UUID: "uuid-" + host, Serial: "S-" + host, MACAddresses: []string{testMAC}},
		TalosVersion: release.Pinned.Talos,
		Hostname:     "talos-" + host,
		Disks: []ichorv1alpha1.TalosMachineDiscoveredDisk{
			{DevPath: "/dev/nvme0n1", Serial: "S4X1NX0", SizeBytes: 1 << 30},
			{DevPath: "/dev/sdb", SizeBytes: 1 << 30},
		},
		Links: []ichorv1alpha1.TalosMachineDiscoveredLink{{Name: testHWLink, PermanentMAC: testMAC}},
	}, nil
}

// readerOf answers the read of each node by its address: configured ones refuse, the others read as testNode.
func readerOf(configured ...string) func(context.Context, string, bool) (nodeReading, error) {
	return func(_ context.Context, endpoint string, _ bool) (nodeReading, error) {
		host, _, _ := net.SplitHostPort(endpoint)

		return fakeNode(host, slices.Contains(configured, host))
	}
}

// scripted answers each question of the terminal in turn: the answers are the values of the first question of
// each call, and the questions asked are kept for the test to look at.
type scripted struct {
	answers []string
	asked   [][]promptQuestion
}

func (s *scripted) ask(qs []promptQuestion, _ map[string]string, _ io.Reader, _ io.Writer) (map[string]string, error) {
	s.asked = append(s.asked, qs)

	if len(s.answers) == 0 {
		return nil, errPromptCancelled
	}

	a := s.answers[0]
	s.answers = s.answers[1:]

	return map[string]string{flagNodeEndpoint: a}, nil
}

func TestHostAddrs(t *testing.T) {
	for _, tc := range []struct {
		prefix      string
		first, last string
		n           int
	}{
		{testScanRange, "192.168.0.1", "192.168.0.254", 254},
		{"10.0.0.0/22", "10.0.0.1", "10.0.3.254", 1022},
		{"10.0.0.8/31", testHostTop, "10.0.0.9", 2},
		{"10.0.0.8/32", testHostTop, testHostTop, 1},
	} {
		got := hostAddrs(netip.MustParsePrefix(tc.prefix))
		if len(got) != tc.n || got[0].String() != tc.first || got[len(got)-1].String() != tc.last {
			t.Errorf("%s: %d addresses from %v to %v, want %d from %s to %s",
				tc.prefix, len(got), got[0], got[len(got)-1], tc.n, tc.first, tc.last)
		}
	}
}

func TestParseScanRange(t *testing.T) {
	if p, err := parseScanRange("192.168.0.7/24"); err != nil || p.String() != testScanRange {
		t.Errorf("a host inside a network is the network: %v, %v", p, err)
	}

	for _, bad := range []string{"10.0.0.0/16", "fd00::/64", testFirstNode, "junk/24"} {
		if _, err := parseScanRange(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestParseScanRange_WiderThanTheLimitSaysSo(t *testing.T) {
	_, err := parseScanRange("10.0.0.0/16")
	if err == nil || !strings.Contains(err.Error(), "wider than a /22") {
		t.Errorf("the refusal does not name the limit: %v", err)
	}
}

func TestWithApidPort(t *testing.T) {
	for in, want := range map[string]string{
		testFirstNode:         "192.168.0.201:50000",
		"192.168.0.201:50001": "192.168.0.201:50001",
		"fd00::1":             "[fd00::1]:50000",
	} {
		if got := withApidPort(in); got != want {
			t.Errorf("withApidPort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIPNetworkOf(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.7.9/24": "192.168.7.0/24",
		"192.168.7.9/23": "192.168.6.0/23",
		"10.1.2.3/16":    "10.1.2.0/24", // wider than a scan: the /24 the workstation is on
	} {
		addr, bits, _ := strings.Cut(in, "/")

		ones, _ := strconv.Atoi(bits)
		ipn := &net.IPNet{IP: net.ParseIP(addr), Mask: net.CIDRMask(ones, 32)}

		p, ok := ipNetworkOf(ipn)
		if !ok || p.String() != want {
			t.Errorf("ipNetworkOf(%s) = %v, %v; want %s", in, p, ok, want)
		}
	}

	if _, ok := ipNetworkOf(&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)}); ok {
		t.Error("a link-local address was taken as the workstation's network")
	}
}

func TestScanPort_FindsTheAddressesThatAccept(t *testing.T) {
	p := netip.MustParsePrefix(testNet)

	got := scanPort(context.Background(), p, time.Second, acceptOnly(testEndpoint7, "192.168.5.9:50000"))
	if want := []netip.Addr{netip.MustParseAddr(testAddr7), netip.MustParseAddr("192.168.5.9")}; !slices.Equal(got, want) {
		t.Errorf("scanPort = %v, want %v", got, want)
	}
}

// A silent host costs one timeout, and a /24 is one wave of connections: with the silent hosts of a whole /24 the
// scan takes about one timeout, not 254 of them.
func TestScanPort_ASilentNetworkTakesAboutOneTimeout(t *testing.T) {
	silent := func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }

	const timeout = 50 * time.Millisecond

	start := time.Now()

	if got := scanPort(context.Background(), netip.MustParsePrefix(testNet), timeout, silent); len(got) != 0 {
		t.Fatalf("a silent network answered: %v", got)
	}

	if took := time.Since(start); took > 10*timeout {
		t.Errorf("a silent /24 took %s with a %s timeout", took, timeout)
	}
}

func TestScanPort_BoundsTheConnectionsInFlight(t *testing.T) {
	var inFlight, peak atomic.Int64

	dial := func(_ context.Context, _ string) error {
		n := inFlight.Add(1)
		for {
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}

		time.Sleep(time.Millisecond)
		inFlight.Add(-1)

		return errors.New("refused")
	}

	scanPort(context.Background(), netip.MustParsePrefix("10.0.0.0/22"), time.Second, dial)

	if peak.Load() > scanConcurrency || peak.Load() < 2 {
		t.Errorf("at most %d connections were in flight at once, want between 2 and %d", peak.Load(), scanConcurrency)
	}
}

func TestNodeChoices_ShowsEveryNodeAndWhyOneCannotBeChosen(t *testing.T) {
	found := []foundNode{
		{endpoint: testEndpoint7, reading: mustNode(t, testAddr7)},
		{endpoint: testEndpoint8, configured: true, refusal: "has a Talos configuration already; init needs a node in maintenance mode"},
		{endpoint: "192.168.5.9:50000", refusal: "runs Talos v1.0.0, but init needs " + release.Pinned.Talos + ": upgrade it first"},
	}

	c := nodeChoices(found)

	if want := "192.168.5.7  talos-192.168.5.7  " + release.Pinned.Talos; c[0].label != want {
		t.Errorf("row label %q, want %q", c[0].label, want)
	}

	if want := "/dev/nvme0n1 serial S4X1NX0 (+1 more), enp1s0 <aa:bb:cc:00:00:01>"; c[0].desc != want {
		t.Errorf("row detail %q, want %q", c[0].desc, want)
	}

	if c[0].value != testEndpoint7 {
		t.Errorf("a node's value is its endpoint, got %q", c[0].value)
	}

	if c[1].value != "" || c[1].label != testConfiguredRow || !strings.Contains(c[1].desc, "Talos configuration already") {
		t.Errorf("a configured node is listed as a row that is not a choice: %+v", c[1])
	}

	if c[2].value != "" || c[2].label != "192.168.5.9  cannot be the first node" {
		t.Errorf("a node that cannot be the first one is listed as a row that is not a choice: %+v", c[2])
	}
}

func mustNode(t *testing.T, host string) nodeReading {
	t.Helper()

	r, err := fakeNode(host, false)
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func TestReadFound_ClassifiesEachAnswer(t *testing.T) {
	s := nodeSearch{ReadNode: readerOf(testAddr8)}
	found := readFound(context.Background(), s, []string{testEndpoint7, testEndpoint8})

	if found[0].refusal != "" || found[0].reading.Hostname != "talos-192.168.5.7" {
		t.Errorf("a maintenance-mode node: %+v", found[0])
	}

	if !found[1].configured || found[1].refusal == "" {
		t.Errorf("a configured node is not marked: %+v", found[1])
	}
}

func TestAskFirstNode_AnAddressSkipsTheScan(t *testing.T) {
	flags := newInitFlags(t)
	s := &scripted{}
	dialed := false

	search := nodeSearch{
		Dial:     func(context.Context, string) error { dialed = true; return nil },
		ReadNode: readerOf(),
		Ask:      s.ask,
	}

	if err := askFirstNode(context.Background(), flags, strings.NewReader(""), io.Discard, search, testAddr7); err != nil {
		t.Fatal(err)
	}

	if got, _ := flags.GetString(flagNodeEndpoint); got != testEndpoint7 || dialed || len(s.asked) != 0 {
		t.Errorf("--node-endpoint %q, dialed %v, %d questions asked", got, dialed, len(s.asked))
	}
}

func TestAskFirstNode_ScansTheNetworkAndSetsThePickedNode(t *testing.T) {
	flags := newInitFlags(t)

	var out bytes.Buffer

	// The list's answer is the endpoint of the row picked.
	s := &scripted{answers: []string{testEndpoint7}}
	search := nodeSearch{
		Dial:     acceptOnly(testEndpoint7, testEndpoint8),
		ReadNode: readerOf(testAddr8),
		Ask:      s.ask,
	}

	if err := askFirstNode(context.Background(), flags, strings.NewReader(""), &out, search, testNet); err != nil {
		t.Fatal(err)
	}

	if got, _ := flags.GetString(flagNodeEndpoint); got != testEndpoint7 {
		t.Errorf("--node-endpoint %q", got)
	}

	if len(s.asked) != 1 || s.asked[0][0].choices == nil {
		t.Fatalf("the nodes were not listed to pick from: %+v", s.asked)
	}

	choices := s.asked[0][0].choices
	if len(choices) != 2 || choices[1].label != testConfiguredRow {
		t.Errorf("the list is %+v", choices)
	}

	if !strings.Contains(out.String(), "Scanning 192.168.5.0/24 for port 50000 (254 addresses)") {
		t.Errorf("the scan is not announced:\n%s", out.String())
	}
}

func TestAskFirstNode_NothingFoundSaysSoAndAsksAgain(t *testing.T) {
	flags := newInitFlags(t)

	var out bytes.Buffer

	s := &scripted{answers: []string{testAddr7}}
	search := nodeSearch{Dial: acceptOnly(), ReadNode: readerOf(), Ask: s.ask}

	if err := askFirstNode(context.Background(), flags, strings.NewReader(""), &out, search, testNet); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "Nothing answered on port 50000 in 192.168.5.0/24 (254 addresses") ||
		!strings.Contains(out.String(), "maintenance mode") {
		t.Errorf("the empty scan does not say what to check:\n%s", out.String())
	}

	if len(s.asked) != 1 || s.asked[0][0].def != testNet {
		t.Fatalf("the question was not asked again, starting from the network: %+v", s.asked)
	}

	if got, _ := flags.GetString(flagNodeEndpoint); got != testEndpoint7 {
		t.Errorf("--node-endpoint %q", got)
	}
}

func TestAskFirstNode_OnlyConfiguredNodesSaysSoAndAsksAgain(t *testing.T) {
	flags := newInitFlags(t)

	var out bytes.Buffer

	s := &scripted{answers: []string{testAddr7}}
	search := nodeSearch{
		Dial:     acceptOnly(testEndpoint8),
		ReadNode: readerOf(testAddr8),
		Ask:      s.ask,
	}

	if err := askFirstNode(context.Background(), flags, strings.NewReader(""), &out, search, testNet); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), testConfiguredRow) || !strings.Contains(out.String(), "None of these nodes can be the first one") {
		t.Errorf("the configured node is not shown and explained:\n%s", out.String())
	}

	if len(s.asked) != 1 {
		t.Errorf("the question was asked %d times again, want once", len(s.asked))
	}
}

// A read the node resets is tried again: the first connection after a scan is sometimes the one reset.
func TestReadFound_RetriesAResetRead(t *testing.T) {
	calls := 0
	s := nodeSearch{ReadNode: func(_ context.Context, endpoint string, wantInventory bool) (nodeReading, error) {
		calls++
		if calls == 1 {
			return nodeReading{}, errors.New("connection reset by peer")
		}

		return fakeNode(testAddr7, false)
	}}

	found := readFound(context.Background(), s, []string{testEndpoint7})

	if calls != 2 || found[0].refusal != "" {
		t.Errorf("%d reads, refusal %q; want two reads and no refusal", calls, found[0].refusal)
	}
}
