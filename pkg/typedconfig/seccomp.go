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

package typedconfig

import (
	"slices"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// SeccompProfile is MachineSeccompProfile.MachineSeccompProfileValue's mirror
// type (internal/tools/mirrorgen/main.go's fieldOverrides): byte-for-byte the
// same fields and JSON tags as specs.LinuxSeccomp (github.com/opencontainers/
// runtime-spec), under a local name so DeepCopyInto/DeepCopy can be
// hand-written below. The generic conversion helpers in substitution.go and
// the generated CRD schema are unaffected by the rename -- both work off the
// JSON shape, which is identical.
//
// Why hand-written: specs.LinuxSeccomp has pointer and slice fields but,
// unlike the other four field overrides (all Kubernetes API types), no
// DeepCopy of its own; controller-gen's object:generate step assumes one
// exists for any struct-shaped field type it doesn't otherwise recognize,
// and errors at compile time rather than generation time when it doesn't.
// The methods can't live on specs.LinuxSeccomp directly -- Go forbids
// methods on types from another package -- and can't live one level up, on
// MachineSeccompProfile itself, either: MachineConfig holds
// MachineSeccompProfiles []*MachineSeccompProfile, and controller-gen's
// generated code for a slice of pointers to a type with a *known* DeepCopy
// method assumes a value-typed element and double-dereferences, which
// doesn't compile. Putting the hand-written methods here, one level down,
// avoids both problems: SeccompProfile is reached only through a plain
// pointer field, never through a pointer slice.
//
// deepCopyLinuxSeccomp and deepCopyLinuxSyscall below copy their known
// pointer/slice fields by name rather than reflecting over the upstream
// type, so they go stale -- silently, by sharing instead of copying -- if
// specs-go ever adds one. seccomp_test.go's TestLinuxSeccompShape guards
// against that by pinning the exact field set each one assumes.
type SeccompProfile specs.LinuxSeccomp

// DeepCopyInto copies the receiver into out.
func (in *SeccompProfile) DeepCopyInto(out *SeccompProfile) {
	v := deepCopyLinuxSeccomp((*specs.LinuxSeccomp)(in))
	*out = SeccompProfile(*v)
}

// DeepCopy returns a deep copy of the receiver.
func (in *SeccompProfile) DeepCopy() *SeccompProfile {
	if in == nil {
		return nil
	}

	out := new(SeccompProfile)
	in.DeepCopyInto(out)

	return out
}

func deepCopyLinuxSeccomp(in *specs.LinuxSeccomp) *specs.LinuxSeccomp {
	if in == nil {
		return nil
	}

	out := *in

	if in.DefaultErrnoRet != nil {
		v := *in.DefaultErrnoRet
		out.DefaultErrnoRet = &v
	}

	out.Architectures = slices.Clone(in.Architectures)
	out.Flags = slices.Clone(in.Flags)

	if in.Syscalls != nil {
		out.Syscalls = make([]specs.LinuxSyscall, len(in.Syscalls))
		for i, sc := range in.Syscalls {
			out.Syscalls[i] = deepCopyLinuxSyscall(sc)
		}
	}

	return &out
}

func deepCopyLinuxSyscall(in specs.LinuxSyscall) specs.LinuxSyscall {
	out := in

	out.Names = slices.Clone(in.Names)

	if in.ErrnoRet != nil {
		v := *in.ErrnoRet
		out.ErrnoRet = &v
	}

	// LinuxSeccompArg (specs-go) is all value fields (uint/uint64/named string), so
	// copying the slice is already a full deep copy with no further recursion needed.
	out.Args = slices.Clone(in.Args)

	return out
}
