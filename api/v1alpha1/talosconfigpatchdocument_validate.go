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

package v1alpha1

import (
	"fmt"
	"slices"
)

// Validate checks what the CRD's two CEL rules on a document check: that Kind names a known kind,
// that the field Kind names is populated, and that no other field is. A document that reached the
// controller without passing admission (stored before the rules, or built in process) is otherwise
// converted to a nil document with no error. It is hand-written: this line's types carry no
// generated Validate.
func (d *TalosConfigPatchDocument) Validate() error {
	fields := []struct {
		kind, field string
		set         bool
	}{
		{"SysctlConfig", "sysctlConfig", d.SysctlConfig != nil},
		{"KernelModuleConfig", "kernelModuleConfig", d.KernelModuleConfig != nil},
		{"Config", "config", d.Config != nil},
		{"VolumeConfig", "volumeConfig", d.VolumeConfig != nil},
		{"UserVolumeConfig", "userVolumeConfig", d.UserVolumeConfig != nil},
		{"RawVolumeConfig", "rawVolumeConfig", d.RawVolumeConfig != nil},
		{"ResolverConfig", "resolverConfig", d.ResolverConfig != nil},
	}

	var want string

	var populated []string

	for _, f := range fields {
		if f.kind == d.Kind {
			want = f.field
		}

		if f.set {
			populated = append(populated, f.field)
		}
	}

	switch {
	case want == "":
		return fmt.Errorf("unknown document kind %q", d.Kind)
	case !slices.Contains(populated, want):
		return fmt.Errorf("document kind %q requires field %q, which is not set (fields set: %v)", d.Kind, want, populated)
	case len(populated) > 1:
		return fmt.Errorf("document kind %q allows only field %q, but %q are set", d.Kind, want, populated)
	}

	return nil
}
