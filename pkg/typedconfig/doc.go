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

// Package typedconfig holds generated mirror types for the Talos machinery
// config documents on Ichor's allow-list (zz_generated.mirror.go), plus
// conversions to and from machinery in both directions
// (zz_generated.conversion.go) and the small, hand-written substitution
// table conversions in substitution.go lean on.
//
//go:generate go run ../../internal/tools/mirrorgen
package typedconfig
