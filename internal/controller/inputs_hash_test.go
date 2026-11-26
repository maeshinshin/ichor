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

package controller

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/render"
)

func inputsHashFor(t *testing.T, sys *render.SystemInputs) string {
	t.Helper()

	cluster := &ichorv1alpha1.TalosCluster{}
	cluster.Name = "c"
	m := &ichorv1alpha1.TalosMachine{}
	m.Spec.NodeLabels = map[string]string{"role": "storage"}

	h, err := desiredInputsHash(cluster, m, nil, sys)
	if err != nil {
		t.Fatal(err)
	}

	return h
}

// TestOIDCTrustChangeMovesInputsHash: a reissued CA or a new ClusterIP changes the render, so it
// must change the hash and not read as node drift (ADR 0088).
func TestOIDCTrustChangeMovesInputsHash(t *testing.T) {
	a := inputsHashFor(t, &render.SystemInputs{OIDCTrust: &render.OIDCTrust{DiscoveryURL: "https://10.96.0.10", CAPEM: "ca"}})
	b := inputsHashFor(t, &render.SystemInputs{OIDCTrust: &render.OIDCTrust{DiscoveryURL: "https://10.96.0.11", CAPEM: "ca"}})

	if a == b {
		t.Fatal("trusts that differ only in ClusterIP gave the same desiredInputsHash")
	}
}

// TestEverySystemInputsFieldMovesInputsHash: a new render.SystemInputs field fails here until
// desiredInputsHash folds it in, so a render input can never read as drift.
func TestEverySystemInputsFieldMovesInputsHash(t *testing.T) {
	// One non-zero value per field; the reflection check below fails for a field missing here.
	set := map[string]func(*render.SystemInputs){
		"RegistryAuth": func(s *render.SystemInputs) { s.RegistryAuth = map[string]render.RegistryAuth{"h": {Password: "x"}} },
		"Storage": func(s *render.SystemInputs) {
			st := ichorv1alpha1.TalosStorage{ObjectMeta: metav1.ObjectMeta{Name: "s", Generation: 3}}
			st.Spec.ClusterRef.Name = "c"
			st.Spec.Nodes = metav1.LabelSelector{MatchLabels: map[string]string{"role": "storage"}}
			s.Storage = []ichorv1alpha1.TalosStorage{st}
		},
		"CARotation": func(s *render.SystemInputs) {
			s.CARotation = &render.CARotation{Stage: "x", New: &secretsbundle.Rotation{}}
		},
		"KeyRotation": func(s *render.SystemInputs) {
			s.KeyRotation = &render.KeyRotation{Stage: "x", New: &secretsbundle.KeyRotation{Secretbox: "k"}}
		},
		"Staged":    func(s *render.SystemInputs) { s.Staged = &render.StagedKubernetes{KubeletVersion: "v1.0.0"} },
		"OIDCTrust": func(s *render.SystemInputs) { s.OIDCTrust = &render.OIDCTrust{DiscoveryURL: "u"} },
	}

	base := inputsHashFor(t, &render.SystemInputs{})
	ty := reflect.TypeFor[render.SystemInputs]()

	for i := range ty.NumField() {
		name := ty.Field(i).Name

		fn, ok := set[name]
		if !ok {
			t.Errorf("SystemInputs.%s has no case here: add one and fold the field into desiredInputsHash", name)

			continue
		}

		sys := &render.SystemInputs{}
		fn(sys)

		if inputsHashFor(t, sys) == base {
			t.Errorf("SystemInputs.%s does not move desiredInputsHash", name)
		}
	}
}
