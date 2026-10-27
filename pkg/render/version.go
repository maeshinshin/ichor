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

package render

// Version identifies the renderer's output for a given set of inputs. Bump it by one in any
// change that makes Render emit different bytes for the same CRs: the controller
// folds it into a machine's input hash, so a new Version reads as a desired-state change and
// is applied, instead of being mistaken for drift when Ichor itself is upgraded.
// TestRenderVersionMatchesOutput fails when the output changes without a bump.
const Version = 1
