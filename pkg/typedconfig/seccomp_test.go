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
	"reflect"
	"slices"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// TestLinuxSeccompShape guards deepCopyLinuxSeccomp and deepCopyLinuxSyscall
// (seccomp.go) against silent aliasing: both start with a shallow `out :=
// *in` and then deep-copy only the pointer/slice fields they know about by
// name. That compiles and round-trips fine today; it would also compile and
// round-trip fine the day specs-go adds a new pointer or slice field, which
// would then be shared between in and out instead of copied. This test
// turns that into a loud failure, naming the field and which function to fix,
// instead of a silently-aliased value nobody notices.
func TestLinuxSeccompShape(t *testing.T) {
	checkFields(t, specs.LinuxSeccomp{}, []string{
		"DefaultAction", "DefaultErrnoRet", "Architectures", "Flags",
		"ListenerPath", "ListenerMetadata", "Syscalls",
	}, "deepCopyLinuxSeccomp")

	checkFields(t, specs.LinuxSyscall{}, []string{
		"Names", "Action", "ErrnoRet", "Args",
	}, "deepCopyLinuxSyscall")

	// LinuxSeccompArg isn't deep-copied field by field: deepCopyLinuxSyscall
	// copies its slice directly, on the grounds that every LinuxSeccompArg
	// field is a value (no pointer/slice/map), so a shallow element copy is
	// already a full one. If that upstream type ever stops being all-value
	// fields, this is the line that needs to change.
	checkFields(t, specs.LinuxSeccompArg{}, []string{
		"Index", "Value", "ValueTwo", "Op",
	}, "deepCopyLinuxSyscall (copies []LinuxSeccompArg by value, assuming no pointer/slice/map fields here)")
}

func checkFields(t *testing.T, upstream any, want []string, fn string) {
	t.Helper()

	typ := reflect.TypeOf(upstream)

	got := make([]string, typ.NumField())
	for i := range got {
		got[i] = typ.Field(i).Name
	}

	gotSorted, wantSorted := slices.Clone(got), slices.Clone(want)
	slices.Sort(gotSorted)
	slices.Sort(wantSorted)

	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("%s's fields are %v, but %s was written assuming %v -- "+
			"check whether the field that changed needs a deep copy, then update both", typ, got, fn, want)
	}
}
