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
	"errors"
	"fmt"
	"strings"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// networkFromFlags turns --nic/--address/--gateway/--dns/--vlan into the approval's network
// settings (ADR 0091). --nic alone is DHCP, any --address makes it static. The backend checks
// the values themselves (the MAC against the machine's discovered ones, the address syntax); this
// only refuses flags that cannot mean anything without --nic.
func networkFromFlags(o machineApproveOptions) (*pb.MachineNetwork, error) {
	if o.NIC == "" {
		if len(o.Addresses) > 0 || o.Gateway != "" || len(o.DNS) > 0 || o.VLAN != 0 {
			return nil, errors.New("--address, --gateway, --dns and --vlan need --nic to say which link they are for")
		}

		return nil, nil
	}

	n := &pb.MachineNetwork{Mac: o.NIC, Addressing: "dhcp", Gateway: o.Gateway, DnsServers: o.DNS, VlanId: o.VLAN}

	if len(o.Addresses) > 0 {
		n.Addressing, n.Addresses = "static", o.Addresses
	} else if o.Gateway != "" {
		return nil, errors.New("--gateway needs a static --address")
	}

	return n, nil
}

// disksFromFlags turns --ephemeral-disk and --user-volume NAME=DISK[,size=SIZE] into disk roles.
// DISK takes the same syntax as --install-disk.
func disksFromFlags(o machineApproveOptions) ([]*pb.MachineDiskRole, error) {
	var out []*pb.MachineDiskRole

	if o.EphemeralDisk != "" {
		d, err := parseDiskSelector("--ephemeral-disk", o.EphemeralDisk)
		if err != nil {
			return nil, err
		}

		out = append(out, &pb.MachineDiskRole{Role: "ephemeral", Disk: diskToPB(d)})
	}

	for _, v := range o.UserVolumes {
		name, rest, ok := strings.Cut(v, "=")
		if !ok || name == "" || rest == "" {
			return nil, fmt.Errorf("--user-volume %q: want NAME=DISK[,size=200GiB]", v)
		}

		disk, size, _ := strings.Cut(rest, ",size=")

		d, err := parseDiskSelector("--user-volume", disk)
		if err != nil {
			return nil, err
		}

		out = append(out, &pb.MachineDiskRole{Role: "userVolume", Name: name, MaxSize: size, Disk: diskToPB(d)})
	}

	return out, nil
}
