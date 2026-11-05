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
	"regexp"
	"strconv"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const installDiskUsage = `"wwid:ID", "serial:SERIAL", a /dev/disk/by-id/ path, a bus path such as /pci0000:00/..., or a device path such as /dev/sda`

// parseDiskSelector reads a disk flag value; flag only names the option in the error. The
// stable identifiers come first in the design's fallback chain; a bare /dev/sdX path works but the
// kernel can renumber it.
func parseDiskSelector(flag, v string) (*ichorv1alpha1.TalosMachineDisk, error) {
	switch {
	case strings.HasPrefix(v, "wwid:") && len(v) > len("wwid:"):
		return &ichorv1alpha1.TalosMachineDisk{WWID: strings.TrimPrefix(v, "wwid:")}, nil
	case strings.HasPrefix(v, "serial:") && len(v) > len("serial:"):
		return &ichorv1alpha1.TalosMachineDisk{Serial: strings.TrimPrefix(v, "serial:")}, nil
	case strings.HasPrefix(v, "/dev/disk/by-id/"):
		return &ichorv1alpha1.TalosMachineDisk{ByIDPath: v}, nil
	case strings.HasPrefix(v, "/dev/"):
		return &ichorv1alpha1.TalosMachineDisk{DevPath: v}, nil
	case strings.HasPrefix(v, "/"):
		return &ichorv1alpha1.TalosMachineDisk{BusPath: v}, nil
	}

	return nil, fmt.Errorf("%s %q: want %s", flag, v, installDiskUsage)
}

var (
	diskFieldExpr   = regexp.MustCompile(`^disk\.(wwid|serial|bus_path|dev_path) == ("(?:[^"\\]|\\.)*")$`)
	diskSymlinkExpr = regexp.MustCompile(`^("(?:[^"\\]|\\.)*") in disk\.symlinks$`)
)

// diskFromExpression is the --install-disk form of a CEL disk selector, or "" when the selector
// is anything but the five equalities Ichor renders itself (diskExpression in pkg/render): a size
// range, a model match or a different symlink has no spec.install.disk representation.
func diskFromExpression(expr string) string {
	expr = strings.TrimSpace(expr)

	if m := diskSymlinkExpr.FindStringSubmatch(expr); m != nil {
		if v, err := strconv.Unquote(m[1]); err == nil && strings.HasPrefix(v, "/dev/disk/by-id/") {
			return v
		}

		return ""
	}

	m := diskFieldExpr.FindStringSubmatch(expr)
	if m == nil {
		return ""
	}

	v, err := strconv.Unquote(m[2])
	if err != nil || v == "" {
		return ""
	}

	switch m[1] {
	case "wwid":
		return "wwid:" + v
	case "serial":
		return "serial:" + v
	case "dev_path":
		if strings.HasPrefix(v, "/dev/") && !strings.HasPrefix(v, "/dev/disk/by-id/") {
			return v
		}
	case "bus_path":
		if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "/dev/") {
			return v
		}
	}

	return ""
}

// installDiskToPB is the parsed --install-disk on the wire.
func installDiskToPB(d *ichorv1alpha1.TalosMachineDisk, image string) *pb.MachineInstall {
	return &pb.MachineInstall{Disk: diskToPB(d), Image: image}
}

// diskToPB is a parsed disk selector on the wire.
func diskToPB(d *ichorv1alpha1.TalosMachineDisk) *pb.MachineDisk {
	return &pb.MachineDisk{Wwid: d.WWID, Serial: d.Serial, ByIdPath: d.ByIDPath, BusPath: d.BusPath, DevPath: d.DevPath}
}
