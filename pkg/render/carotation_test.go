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

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// requireAcceptedCAs fails unless list holds exactly the certificates in want, in any order.
func requireAcceptedCAs(t *testing.T, what string, list []*x509.PEMEncodedCertificate, want [][]byte) {
	t.Helper()

	got := make([]string, 0, len(list))
	for _, c := range list {
		got = append(got, string(c.Crt))
	}

	wantStr := make([]string, 0, len(want))
	for _, c := range want {
		wantStr = append(wantStr, string(c))
	}

	slices.Sort(got)
	slices.Sort(wantStr)

	if !slices.Equal(got, wantStr) {
		t.Errorf("%s: accepted CAs differ from the expected ones (got %d, want %d)", what, len(got), len(wantStr))
	}
}

// TestCARotationStagesRenderTheRightIssuerAndAcceptedCAs pins, for both roles, which CA issues and
// which is accepted in each stage, for the Talos API CA and the Kubernetes API CA.
func TestCARotationStagesRenderTheRightIssuerAndAcceptedCAs(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewRotation(time.Now(), testContract(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{roleControlPlane, roleWorker} {
		m := testMachine("node-"+role, role, nil)

		type want struct {
			talosIssuer, k8sIssuer []byte
			talosAccepted          [][]byte
			k8sAccepted            [][]byte
		}

		stages := map[string]want{
			ichorv1alpha1.CARotationStageAccepting: {
				old.Certs.OS.Crt, old.Certs.K8s.Crt,
				[][]byte{next.Talos.Crt}, [][]byte{old.Certs.K8s.Crt, next.Kubernetes.Crt},
			},
			ichorv1alpha1.CARotationStageIssuing: {
				next.Talos.Crt, next.Kubernetes.Crt,
				[][]byte{old.Certs.OS.Crt}, [][]byte{next.Kubernetes.Crt, old.Certs.K8s.Crt},
			},
			ichorv1alpha1.CARotationStageDropping: {
				next.Talos.Crt, next.Kubernetes.Crt, nil, [][]byte{next.Kubernetes.Crt},
			},
		}

		for stage, w := range stages {
			cfg, err := BuildConfig(cluster, m, old, nil, &SystemInputs{CARotation: &CARotation{Stage: stage, New: next}})
			if err != nil {
				t.Fatalf("%s %s: %v", role, stage, err)
			}

			sec := cfg.Machine().Security()
			if !bytes.Equal(sec.IssuingCA().Crt, w.talosIssuer) {
				t.Errorf("%s %s: wrong Talos issuing CA", role, stage)
			}

			if role == roleControlPlane && len(sec.IssuingCA().Key) == 0 {
				t.Errorf("%s %s: a control plane must carry the issuing key", role, stage)
			}

			if role == roleWorker && len(sec.IssuingCA().Key) != 0 {
				t.Errorf("%s %s: a worker must carry the issuing certificate only", role, stage)
			}

			requireAcceptedCAs(t, role+" "+stage+": Talos", sec.AcceptedCAs(), w.talosAccepted)

			k8s := cfg.K8sAPIServerCAConfig()
			if k8s == nil {
				t.Fatalf("%s %s: no Kubernetes CA document", role, stage)
			}

			requireAcceptedCAs(t, role+" "+stage+": Kubernetes", k8s.AcceptedCAs(), w.k8sAccepted)

			if role == roleControlPlane && !bytes.Equal(k8s.IssuingCA().Crt, w.k8sIssuer) {
				t.Errorf("%s %s: wrong Kubernetes issuing CA", role, stage)
			}
		}
	}
}

// TestCARotationLastStagesEqualTheFinalConfiguration is the property that lets the machine
// controller see no drift when a rotation ends: what the nodes run after the last pass is exactly
// what the replaced bundle renders.
func TestCARotationLastStagesEqualTheFinalConfiguration(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewRotation(time.Now(), testContract(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{roleControlPlane, roleWorker} {
		m := testMachine("node-"+role, role, nil)

		_, final, err := Render(cluster, m, next.Apply(old), nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		for _, stage := range []string{ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing} {
			_, got, err := Render(cluster, m, old, nil, &SystemInputs{CARotation: &CARotation{Stage: stage, New: next}})
			if err != nil {
				t.Fatal(err)
			}

			if got != final {
				t.Errorf("%s: the %s configuration differs from the replaced bundle's (drift)", role, stage)
			}
		}

		// Accepting must differ from the unrotated configuration, or nothing would be applied.
		_, before, _ := Render(cluster, m, old, nil, nil)
		_, accepting, _ := Render(cluster, m, old, nil,
			&SystemInputs{CARotation: &CARotation{Stage: ichorv1alpha1.CARotationStageAccepting, New: next}})

		if before == accepting {
			t.Errorf("%s: the Accepting configuration equals the original", role)
		}
	}
}

func TestCARotationRefusesAnUnknownStage(t *testing.T) {
	next, _ := secretsbundle.NewRotation(time.Now(), testContract(t))

	_, _, err := Render(testCluster("true"), testMachine("n", roleWorker, nil), testBundle(t), nil,
		&SystemInputs{CARotation: &CARotation{Stage: "Done", New: next}})
	if err == nil {
		t.Fatal("a stage no configuration is rendered for must be an error")
	}
}
