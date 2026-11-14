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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"

	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/uiapi"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The scan of a network for Talos's API port. A /24 is one wave of scanConcurrency connections, so it takes
// about one scanDialTimeout whatever answers; the widest network scanned is a /22, four waves.
const (
	scanPrefixMin   = 22
	scanConcurrency = 256
	scanDialTimeout = time.Second
	readConcurrency = 8
	readTimeout     = 10 * time.Second
	readRetryPause  = 250 * time.Millisecond
	apidPort        = constants.ApidPort
)

// dialFunc attempts a connection to addr ("host:port") and closes it when it succeeds. The scan's network is
// one; a test puts in its own.
type dialFunc func(ctx context.Context, addr string) error

// tcpDial is the scan's connection attempt.
func tcpDial(ctx context.Context, addr string) error {
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}

	return c.Close()
}

// nodeSearch is what the first node's question reads from outside itself: the scan's connections (Dial), the
// read init makes of a node (ReadNode), and the terminal questions (Ask, askPrompts on a terminal).
type nodeSearch struct {
	Dial     dialFunc
	ReadNode func(ctx context.Context, endpoint string, wantInventory bool) (nodeReading, error)
	Ask      func(qs []promptQuestion, seed map[string]string, in io.Reader, out io.Writer) (map[string]string, error)
}

// firstNodeSearch is the search the interactive question uses; a test replaces it.
var firstNodeSearch = nodeSearch{Dial: tcpDial, ReadNode: readNodeOverMaintenance, Ask: askPrompts}

// workstationNetwork is the IPv4 network of the workstation's first interface that is up and not loopback: the
// default range. A shortcut: the first such interface wins, so a VPN or a container bridge listed first gives
// the wrong default, and the operator types the range instead; drop it with a choice of interface.
var workstationNetwork = func() (netip.Prefix, bool) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return netip.Prefix{}, false
	}

	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}

		for _, a := range addrs {
			if p, ok := ipNetworkOf(a); ok {
				return p, true
			}
		}
	}

	return netip.Prefix{}, false
}

// ipNetworkOf is the network of an interface address: an IPv4 address that is not link-local, with its
// prefix. A network wider than a scannable one is narrowed to the /24 the address is on.
func ipNetworkOf(a net.Addr) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(a.String()) // a net.IPNet prints as address/length
	if err != nil || !p.Addr().Is4() || p.Addr().IsLinkLocalUnicast() {
		return netip.Prefix{}, false
	}

	if p.Bits() < scanPrefixMin {
		p = netip.PrefixFrom(p.Addr(), 24) //nolint:mnd
	}

	return p.Masked(), true
}

// parseScanRange is the network the first-node question takes: an IPv4 CIDR no wider than a /scanPrefixMin.
func parseScanRange(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%q is not an IPv4 network in CIDR form, like 192.168.0.0/24", s)
	}

	if p.Bits() < scanPrefixMin {
		return netip.Prefix{}, fmt.Errorf("%s is wider than a /%d and would take too long to scan; type a smaller network, or a node's address",
			p, scanPrefixMin)
	}

	return p.Masked(), nil
}

// withApidPort is an address with the Talos API port added when it names none.
func withApidPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}

	return net.JoinHostPort(addr, strconv.Itoa(apidPort))
}

// hostAddrs lists the addresses of p a node can have: all of them but the network and broadcast addresses,
// except in a /31 or /32.
func hostAddrs(p netip.Prefix) []netip.Addr {
	p = p.Masked()
	last := lastAddr(p)

	var out []netip.Addr

	for a := p.Addr(); p.Contains(a); a = a.Next() {
		if p.Bits() < 31 && (a == p.Addr() || a == last) {
			continue
		}

		out = append(out, a)

		if a == last {
			break
		}
	}

	return out
}

// lastAddr is the broadcast address of an IPv4 prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= host

	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// eachBounded calls fn for 0 to n-1, at most limit at a time, and returns once they have all returned.
func eachBounded(n, limit int, fn func(i int)) {
	var g errgroup.Group

	g.SetLimit(limit)

	for i := range n {
		g.Go(func() error { fn(i); return nil })
	}

	_ = g.Wait()
}

// scanPort returns the addresses of p that accept a connection on the Talos API port, in address order. Each attempt
// gets timeout, and at most scanConcurrency run at once.
func scanPort(ctx context.Context, p netip.Prefix, timeout time.Duration, dial dialFunc) []netip.Addr {
	addrs := hostAddrs(p)
	open := make([]bool, len(addrs))

	eachBounded(len(addrs), scanConcurrency, func(i int) {
		actx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		open[i] = dial(actx, net.JoinHostPort(addrs[i].String(), strconv.Itoa(apidPort))) == nil
	})

	var found []netip.Addr

	for i, a := range addrs {
		if open[i] {
			found = append(found, a)
		}
	}

	return found
}

// foundNode is a node that answered the scan. refusal is why init cannot take it as the first node; empty
// when it can.
type foundNode struct {
	endpoint   string
	reading    nodeReading
	configured bool
	refusal    string
}

// readFound asks each address that answered who it is, with the same read init makes of the first node.
func readFound(ctx context.Context, s nodeSearch, endpoints []string) []foundNode {
	found := make([]foundNode, len(endpoints))

	eachBounded(len(endpoints), readConcurrency, func(i int) {
		rctx, cancel := context.WithTimeout(ctx, readTimeout)
		defer cancel()

		f := foundNode{endpoint: endpoints[i]}

		var err error
		// shortcut: one retry, after a pause, for a read the node resets: a node that answered the scan has reset
		// its first connection after it in some runs. Drop it once the cause is known.
		for attempt := range 2 {
			if attempt > 0 {
				select {
				case <-rctx.Done():
				case <-time.After(readRetryPause):
				}
			}

			f.reading, err = s.ReadNode(rctx, endpoints[i], true)
			if err == nil || errors.Is(err, controller.ErrAlreadyConfigured) {
				break
			}
		}

		switch {
		case errors.Is(err, controller.ErrAlreadyConfigured):
			f.configured = true
			f.refusal = "has a Talos configuration already; init needs a node in maintenance mode"
		case err != nil:
			f.refusal = "answers on port " + strconv.Itoa(apidPort) + ", but its API could not be read: " + err.Error()
		case f.reading.TalosVersion != "" && release.Compare(f.reading.TalosVersion, release.Pinned.Talos) == release.Behind:
			f.refusal = fmt.Sprintf("runs Talos %s, but init needs %s: upgrade it first", f.reading.TalosVersion, release.Pinned.Talos)
		case f.reading.Identity.UUID == "":
			f.refusal = "reports no SMBIOS UUID, which init needs"
		}

		found[i] = f
	})

	return found
}

// nodeChoices lists the nodes that answered: each with its address, hostname and Talos version, and under it
// the system disk with its serial and the first link with its MAC. A node that cannot be the first one is listed
// too, as a row that is not a choice and says why.
func nodeChoices(found []foundNode) []promptChoice {
	choices := make([]promptChoice, len(found))

	for i, f := range found {
		host, _, _ := net.SplitHostPort(f.endpoint)

		switch {
		case f.configured:
			choices[i] = promptChoice{label: host + "  already configured", desc: f.refusal}

			continue
		case f.refusal != "":
			choices[i] = promptChoice{label: host + "  cannot be the first node", desc: f.refusal}

			continue
		}

		choices[i] = promptChoice{
			label: host + "  " + cmp.Or(f.reading.Hostname, "-") + "  " + f.reading.TalosVersion,
			value: f.endpoint,
			desc:  nodeDetail(f.reading),
		}
	}

	return choices
}

// nodeDetail is the disk and the link of a node, the first of each with how many more it has.
func nodeDetail(r nodeReading) string {
	if r.InventoryErr != nil {
		return "its disks and links could not be read"
	}

	var parts []string

	if len(r.Disks) == 0 {
		parts = append(parts, "no disk reported")
	} else {
		d := r.Disks[0].DevPath
		if r.Disks[0].Serial != "" {
			d += " serial " + r.Disks[0].Serial
		}

		parts = append(parts, d+moreSuffix(len(r.Disks)))
	}

	if len(r.Links) == 0 {
		parts = append(parts, "no link reported")
	} else {
		parts = append(parts, uiapi.LinkLabel(r.Links[0])+moreSuffix(len(r.Links)))
	}

	return strings.Join(parts, ", ")
}

// moreSuffix is " (+N more)" for a list of n, and nothing for a single entry.
func moreSuffix(n int) string {
	if n <= 1 {
		return ""
	}

	return fmt.Sprintf(" (+%d more)", n-1)
}

// firstNodeQuestion asks for a network to scan or an address to take as it is, starting from def.
func firstNodeQuestion(def string) promptQuestion {
	return promptQuestion{
		flag:  flagNodeEndpoint,
		label: "First node",
		def:   def,
		help: func(map[string]string) string {
			return fmt.Sprintf("a network (CIDR, up to a /%d) is scanned for Talos nodes on port %d and listed to pick from; "+
				"an address skips the scan", scanPrefixMin, apidPort)
		},
		check: func(a string) error {
			if !strings.Contains(a, "/") {
				return answerRequired(a)
			}

			_, err := parseScanRange(a)

			return err
		},
	}
}

// defaultScanRange is the network the first-node question starts with: the workstation's, or none.
func defaultScanRange() string {
	if p, ok := workstationNetwork(); ok {
		return p.String()
	}

	return ""
}

// askFirstNode sets --node-endpoint from the first node's answer. An address is taken as typed, with the Talos
// API port when it names none. A network is scanned and its nodes listed to pick from; a scan that finds no node
// that can be the first one says so, and asks again.
func askFirstNode(ctx context.Context, flags *pflag.FlagSet, r io.Reader, w io.Writer, s nodeSearch, answer string) error {
	for {
		if !strings.Contains(answer, "/") {
			return flags.Set(flagNodeEndpoint, withApidPort(answer))
		}

		p, err := parseScanRange(answer)
		if err != nil {
			return err
		}

		n := len(hostAddrs(p))
		start := time.Now()
		_, _ = fmt.Fprintf(w, "Scanning %s for port %d (%d addresses)...\n", p, apidPort, n)

		open := scanPort(ctx, p, scanDialTimeout, s.Dial)
		endpoints := make([]string, len(open))

		for i, a := range open {
			endpoints[i] = net.JoinHostPort(a.String(), strconv.Itoa(apidPort))
		}

		found := readFound(ctx, s, endpoints)

		if err := ctx.Err(); err != nil {
			return err
		}

		choices := nodeChoices(found)
		selectable := slices.ContainsFunc(found, func(f foundNode) bool { return f.refusal == "" })

		if len(found) == 0 {
			_, _ = fmt.Fprintf(w, "Nothing answered on port %d in %s (%d addresses, %s). A node waiting for init is in maintenance "+
				"mode and listens on that port: check that it is powered on and on this network. Type another network, or a node's address.\n",
				apidPort, p, n, time.Since(start).Round(100*time.Millisecond)) //nolint:mnd
		}

		if len(found) > 0 && !selectable {
			for _, c := range choices {
				_, _ = fmt.Fprintf(w, "  %s  %s\n", c.label, c.desc)
			}

			_, _ = fmt.Fprintln(w, "None of these nodes can be the first one. Type another network, or a node's address.")
		}

		if !selectable {
			got, err := s.Ask([]promptQuestion{firstNodeQuestion(answer)}, nil, r, w)
			if err != nil {
				return err
			}

			answer = got[flagNodeEndpoint]

			continue
		}

		got, err := s.Ask([]promptQuestion{{
			flag:  flagNodeEndpoint,
			label: "First node",
			help: func(map[string]string) string {
				return "the first control plane is installed on the node you pick; a node already configured cannot be picked"
			},
			choices:   choices,
			noDefault: true,
		}}, nil, r, w)
		if err != nil {
			return err
		}

		return flags.Set(flagNodeEndpoint, got[flagNodeEndpoint])
	}
}
