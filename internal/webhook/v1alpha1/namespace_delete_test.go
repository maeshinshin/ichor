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
	"context"
	"testing"

	"github.com/maeshinshin/ichor/pkg/deleteguard"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	kindCluster = "TalosCluster"
	kindMachine = "TalosMachine"
)

// TestNamespaceControllerIsToldWhyItsDeleteIsRefused guards: deleting a namespace that holds a TalosCluster or
// TalosMachine is refused with the namespace message, not the bare annotation hint, and is not exempted: the
// same refusal reaches any other user with the plain message, and the annotation still admits the delete.
func TestNamespaceControllerIsToldWhyItsDeleteIsRefused(t *testing.T) {
	deletes := map[string]func(ctx context.Context, annotations map[string]string) error{
		kindCluster: func(ctx context.Context, a map[string]string) error {
			_, err := (&TalosClusterCustomValidator{}).ValidateDelete(ctx,
				&ichorv1alpha1.TalosCluster{Annotations: a})

			return err
		},
		kindMachine: func(ctx context.Context, a map[string]string) error {
			_, err := (&TalosMachineCustomValidator{}).ValidateDelete(ctx,
				&ichorv1alpha1.TalosMachine{Annotations: a})

			return err
		},
	}

	for kind, del := range deletes {
		err := del(userRequestCtx(nsControllerUser), nil)
		if err == nil || err.Error() != deleteguard.NamespaceDeleteMessage {
			t.Errorf("%s as namespace-controller: got %v, want %q", kind, err, deleteguard.NamespaceDeleteMessage)
		}

		err = del(userRequestCtx(userAlice), nil)
		if err == nil || err.Error() == deleteguard.NamespaceDeleteMessage {
			t.Errorf("%s as another user: got %v, want the plain annotation refusal", kind, err)
		}

		consent := map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
		if err := del(userRequestCtx(nsControllerUser), consent); kind == kindCluster && err != nil {
			t.Errorf("%s with the annotation as namespace-controller: %v", kind, err)
		}
	}
}
