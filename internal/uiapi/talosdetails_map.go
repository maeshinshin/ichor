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
	"net/netip"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

// The functions below turn Talos resource specs into the wire messages of
// GetNodeTalosDetails. Each one copies named fields into a fresh message: a
// spec is never marshalled or embedded whole, so a field Talos adds later (or
// one that carries a key, like WireguardSpec.PrivateKey or a volume's
// encryption keys) cannot reach the browser by accident (docs/adr/0073).

const redactedValue = "***"

// securityFromSpec fills the boot security fields of the system section from the SecurityState
// spec and the unredacted kernel command line (ADR 0123). A node without a SecurityState (nil)
// leaves the fields unset, so the view shows nothing rather than a false "no SecureBoot".
func securityFromSpec(out *pb.TalosSystemSection, spec *runtime.SecurityStateSpec, cmdline string) {
	if spec == nil {
		return
	}

	out.SecureBoot, out.BootedWithUki, out.ModuleSignatureEnforced = spec.SecureBoot, spec.BootedWithUKI, spec.ModuleSignatureEnforced
	out.UkiSigningKeyFingerprint = spec.UKISigningKeyFingerprint
	out.KernelLockdown = secureboot.LockdownLevel(cmdline, spec.SecureBoot)
}

// sensitiveArgFragments mark a kernel argument whose value may be a
// credential. Matching is on the lower-cased key.
var sensitiveArgFragments = []string{"token", "secret", "key", "pass", "auth", "cred", "config", "siderolink", "environment"}

// redactKernelArgs keeps every argument's key and replaces the value of any
// argument that could carry a credential (talos.config=<url with token>,
// siderolink.api=...?jointoken=..., ...) with a fixed mask. An argument with
// no value is kept as is.
func redactKernelArgs(args []string) []string {
	out := make([]string, 0, len(args))

	for _, a := range args {
		key, _, hasValue := strings.Cut(a, "=")
		if !hasValue {
			out = append(out, a)

			continue
		}

		lower := strings.ToLower(key)
		redact := false

		for _, f := range sensitiveArgFragments {
			if strings.Contains(lower, f) {
				redact = true

				break
			}
		}

		if redact {
			out = append(out, key+"="+redactedValue)
		} else {
			out = append(out, a)
		}
	}

	return out
}

// permanentMAC is the burned-in address of a physical link, the one spec.network.mac matches; it
// is empty for a virtual link, which has none.
func permanentMAC(spec *network.LinkStatusSpec) string {
	if len(spec.PermanentAddr) == 0 {
		return ""
	}

	return spec.PermanentAddr.String()
}

func linkFromSpec(name string, spec *network.LinkStatusSpec) *pb.TalosLink {
	return &pb.TalosLink{
		Name:             name,
		Mac:              spec.HardwareAddr.String(),
		PermanentMac:     permanentMAC(spec),
		OperationalState: spec.OperationalState.String(),
		LinkUp:           spec.LinkState,
		Mtu:              spec.MTU,
		SpeedMbit:        int32(spec.SpeedMegabits), //nolint:gosec // link speeds fit in int32
		Kind:             spec.Kind,
		Driver:           spec.Driver,
	}
}

// diskType names the medium: cdrom, nvme (by transport), hdd/ssd (by the
// rotational flag), or the transport itself when none of those apply.
func diskType(transport string, rotational, cdrom bool) string {
	switch {
	case cdrom:
		return "cdrom"
	case strings.EqualFold(transport, "nvme"):
		return "nvme"
	case rotational:
		return "hdd"
	case transport != "":
		return "ssd"
	default:
		return ""
	}
}

func diskFromSpec(id string, spec *block.DiskSpec, systemDiskID string) *pb.TalosDisk {
	return &pb.TalosDisk{
		Id: id, DevPath: spec.DevPath, Model: spec.Model, Serial: spec.Serial,
		SizeBytes:  int64(spec.Size), //nolint:gosec // disk sizes fit in int64
		Type:       diskType(spec.Transport, spec.Rotational, spec.CDROM),
		SystemDisk: systemDiskID != "" && id == systemDiskID,
		ReadOnly:   spec.Readonly,
	}
}

// volumeFromSpec reports only whether the volume is encrypted, never the
// provider's key slots or key material.
func volumeFromSpec(id string, spec *block.VolumeStatusSpec) *pb.TalosVolume {
	return &pb.TalosVolume{
		Id: id, Type: spec.Type.String(), Phase: spec.Phase.String(),
		Location: spec.Location, MountLocation: spec.MountLocation,
		SizeBytes:    int64(spec.Size), //nolint:gosec // volume sizes fit in int64
		Filesystem:   spec.Filesystem.String(),
		Encrypted:    spec.EncryptionProvider != block.EncryptionProviderNone,
		ErrorMessage: spec.ErrorMessage,
	}
}

// isDefaultRoute reports a route to 0.0.0.0/0 or ::/0 with a gateway.
func isDefaultRoute(dst netip.Prefix, gw netip.Addr) bool {
	return gw.IsValid() && (!dst.IsValid() || dst.Bits() == 0)
}
