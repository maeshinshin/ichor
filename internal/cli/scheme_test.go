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

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestCLISchemeKnowsEveryLocalModeType: local mode runs the uiapi server
// in-process on a client built from cliScheme, so every type that server
// (and the CLI's own commands) creates or reads must be registered.
func TestCLISchemeKnowsEveryLocalModeType(t *testing.T) {
	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	for _, obj := range []client.Object{
		&authv1.SelfSubjectAccessReview{}, // override and diagnostics checks
		&corev1.Event{},                   // override and diagnostics audit events
		&corev1.Secret{},
		&corev1.Namespace{},
		&corev1.Node{},
		&ichorv1alpha1.TalosCluster{},
		&ichorv1alpha1.TalosMachine{},
		&ichorv1alpha1.TalosConfigPatch{},
		&ichorv1alpha1.TalosOperation{},
		&ichorv1alpha1.IchorInstallation{},
	} {
		if _, _, err := scheme.ObjectKinds(obj); err != nil {
			t.Errorf("%T is not registered in cliScheme: %v", obj, err)
		}
	}
}
