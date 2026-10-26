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
	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// PatchToWire converts a TalosConfigPatch to the wire Patch message, so the
// CLI builds CreatePatch/UpdatePatch/PreviewImpact requests from a typed CR
// through the exact conversion the backend uses to answer reads.
func PatchToWire(p *ichorv1alpha1.TalosConfigPatch) (*pb.Patch, error) { return patchToPB(p) }
