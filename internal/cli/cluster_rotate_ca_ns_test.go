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
	"testing"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// testOtherNS is a controller namespace other than the default.
const testOtherNS = "other"

// rotate ca hands off credentials in apiOpts.Namespace and must follow the operation in that same namespace, not in
// whichever namespace the created operation's ref happens to carry.
func TestRotateCAFollowsTheOperationInTheNamespaceItHandsOffIn(t *testing.T) {
	old := apiOpts.Namespace
	apiOpts.Namespace = testOtherNS

	t.Cleanup(func() { apiOpts.Namespace = old })

	got := rotateCAOperation(&pb.ObjectRef{Namespace: ichorControllerNamespace, Name: "op"})
	if got.Namespace != testOtherNS || got.Name != "op" {
		t.Fatalf("follows %s/%s, want other/op", got.Namespace, got.Name)
	}
}
