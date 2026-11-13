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
	"io"
	"net/netip"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// What discovery scans is one of these.
const (
	discoveryKindAddress = "address"
	discoveryKindRange   = "range"
)

// printDiscoveryEdit prints the answer to an add or remove: one ✓ line for a person, the wire
// message on -o yaml|json. A just added address that already runs a Talos configuration is a
// ! line: discovery reached it and did not add the node.
func printDiscoveryEdit(out io.Writer, msg proto.Message, remove bool, target string, ref *pb.ObjectRef, alreadyConfigured []string) error {
	if outputFormat != outputHuman {
		return writeMessage(out, outputFormat, msg)
	}

	h := newHuman(out)

	if remove {
		h.done("removed %s from the discovery of %s", target, ref.GetName())
	} else {
		h.done("added %s to the discovery of %s", target, ref.GetName())
	}

	if slices.Contains(alreadyConfigured, target) {
		h.warn(target + " already runs a Talos configuration, so discovery does not add it as a machine; " +
			"if it is a member of this cluster's Talos cluster run `ichor adopt --cluster " + ref.GetName() + " --add-machine " + target + "`, " +
			"if another cluster configured it run `ichor reclaim node " + target + " --talosconfig FILE --cluster " + ref.GetName() + "`")
	}

	return h.Err()
}

// renderDiscoveryList is the table of what the cluster scans and what each address or range found.
// The API keeps no time of the last probe, so the table has none.
func renderDiscoveryList(h *human, cl *pb.Cluster, machines []*pb.Machine) error {
	rows := make([][]string, 0, len(cl.GetDiscoveryAddresses())+len(cl.GetDiscoveryCidrs()))

	for _, a := range cl.GetDiscoveryAddresses() {
		found := foundAt(machines, func(ip netip.Addr) bool { return ip.String() == a })
		if len(found) == 0 && slices.Contains(cl.GetAlreadyConfiguredAddresses(), a) {
			found = []string{"already configured"}
		}

		rows = append(rows, []string{a, discoveryKindAddress, strings.Join(found, ", ")})
	}

	for _, r := range cl.GetDiscoveryCidrs() {
		p, err := netip.ParsePrefix(r)
		if err != nil {
			rows = append(rows, []string{r, discoveryKindRange, ""})

			continue
		}

		found := foundAt(machines, p.Contains)

		cell := strings.Join(found, ", ")
		if len(found) > 3 {
			cell = fmt.Sprintf("%d machines", len(found))
		}

		rows = append(rows, []string{r, discoveryKindRange, cell})
	}

	h.Table([]string{"address/cidr", colKind, "found"}, rows)

	return h.Err()
}

// foundAt is the short names of the machines whose endpoint is an address that match accepts.
func foundAt(machines []*pb.Machine, match func(netip.Addr) bool) []string {
	var names []string

	for _, m := range machines {
		if ip, ok := endpointAddr(m.GetEndpoint()); ok && match(ip) {
			names = append(names, m.GetRef().GetName())
		}
	}

	return shortNames(names)
}

// endpointAddr is the IP of a machine's endpoint, "10.0.0.10:50000" or "10.0.0.10".
func endpointAddr(endpoint string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(endpoint); err == nil {
		return ap.Addr(), true
	}

	ip, err := netip.ParseAddr(endpoint)

	return ip, err == nil
}
