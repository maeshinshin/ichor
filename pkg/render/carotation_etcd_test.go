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
	"encoding/pem"
	"strings"
	"testing"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// pemCerts returns the DER of every certificate block in crt, in order.
func pemCerts(crt []byte) [][]byte {
	var out [][]byte

	for {
		var block *pem.Block

		block, crt = pem.Decode(crt)
		if block == nil {
			return out
		}

		out = append(out, block.Bytes)
	}
}

func firstDER(crt []byte) []byte { return pemCerts(crt)[0] }

var aggregatorOnly = []string{ichorv1alpha1.CARotationTargetAggregator}

// TestCARotationEtcdStagesRenderABundleWithTheSigningCAFirst pins the etcd CA field of a control
// plane in each stage (ADR 0120): both certificates while both are trusted, the signing CA first
// with its own key (Talos signs with the first block and the key), the new one alone at the end.
func TestCARotationEtcdStagesRenderABundleWithTheSigningCAFirst(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewRotationFor(time.Now(), testContract(t), []string{ichorv1alpha1.CARotationTargetEtcd})
	if err != nil {
		t.Fatal(err)
	}

	m := testMachine("cp", roleControlPlane, nil)

	for _, tc := range []struct {
		stage    string
		order    [][]byte // certificates expected in the field, first = signer
		keyOwner []byte   // the key must be this CA's key
	}{
		{ichorv1alpha1.CARotationStageAccepting, [][]byte{old.Certs.Etcd.Crt, next.Etcd.Crt}, old.Certs.Etcd.Key},
		{ichorv1alpha1.CARotationStageIssuing, [][]byte{next.Etcd.Crt, old.Certs.Etcd.Crt}, next.Etcd.Key},
		{ichorv1alpha1.CARotationStageDropping, [][]byte{next.Etcd.Crt}, next.Etcd.Key},
		{ichorv1alpha1.CARotationStageFinalizing, [][]byte{next.Etcd.Crt}, next.Etcd.Key},
	} {
		cfg, err := BuildConfig(cluster, m, old, nil, &SystemInputs{CARotation: &CARotation{Stage: tc.stage, New: next}})
		if err != nil {
			t.Fatalf("%s: %v", tc.stage, err)
		}

		ca := cfg.Cluster().Etcd().CA()
		if ca == nil {
			t.Fatalf("%s: no etcd CA", tc.stage)
		}

		got := pemCerts(ca.Crt)
		if len(got) != len(tc.order) {
			t.Fatalf("%s: the etcd CA field holds %d certificates, want %d", tc.stage, len(got), len(tc.order))
		}

		for i, want := range tc.order {
			if !bytes.Equal(got[i], firstDER(want)) {
				t.Errorf("%s: certificate %d of the etcd CA field is not the expected CA", tc.stage, i)
			}
		}

		if !bytes.Equal(ca.Key, tc.keyOwner) {
			t.Errorf("%s: the etcd CA key is not the key of the first certificate", tc.stage)
		}

		// A CA that is not a target renders exactly as before.
		sec := cfg.Machine().Security()
		if !bytes.Equal(sec.IssuingCA().Crt, old.Certs.OS.Crt) || len(sec.AcceptedCAs()) != 0 {
			t.Errorf("%s: the Talos CA changed in an etcd-only rotation", tc.stage)
		}
	}
}

// TestCARotationAggregatorStagesUseTheAcceptedList pins the aggregator CA document of a control plane:
// Talos adds the issuing CA to the accepted list itself, so the list holds the other CA.
func TestCARotationAggregatorStagesUseTheAcceptedList(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewRotationFor(time.Now(), testContract(t), aggregatorOnly)
	if err != nil {
		t.Fatal(err)
	}

	m := testMachine("cp", roleControlPlane, nil)
	oldAgg, newAgg := old.Certs.K8sAggregator.Crt, next.Aggregator.Crt

	for _, tc := range []struct {
		stage    string
		issuer   []byte
		accepted [][]byte // what the apiserver trusts, issuing CA included
	}{
		{ichorv1alpha1.CARotationStageAccepting, oldAgg, [][]byte{oldAgg, newAgg}},
		{ichorv1alpha1.CARotationStageIssuing, newAgg, [][]byte{newAgg, oldAgg}},
		{ichorv1alpha1.CARotationStageDropping, newAgg, [][]byte{newAgg}},
	} {
		cfg, err := BuildConfig(cluster, m, old, nil, &SystemInputs{CARotation: &CARotation{Stage: tc.stage, New: next}})
		if err != nil {
			t.Fatalf("%s: %v", tc.stage, err)
		}

		doc := cfg.K8sAggregatorCAConfig()
		if doc == nil {
			t.Fatalf("%s: no aggregator CA document", tc.stage)
		}

		if !bytes.Equal(doc.IssuingCA().Crt, tc.issuer) {
			t.Errorf("%s: wrong aggregator issuing CA", tc.stage)
		}

		requireAcceptedCAs(t, tc.stage+": aggregator", doc.AcceptedCAs(), tc.accepted)

		// The API server's own CA is untouched in an aggregator-only rotation.
		if !bytes.Equal(cfg.K8sAPIServerCAConfig().IssuingCA().Crt, old.Certs.K8s.Crt) {
			t.Errorf("%s: the Kubernetes CA changed in an aggregator-only rotation", tc.stage)
		}
	}
}

// TestCARotationEtcdAndAggregatorChangeNoWorker: neither CA is in a worker's configuration, so a
// rotation that only has them leaves every worker at the configuration it runs (no apply, no drift).
func TestCARotationEtcdAndAggregatorChangeNoWorker(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewRotationFor(time.Now(), testContract(t),
		[]string{ichorv1alpha1.CARotationTargetEtcd, ichorv1alpha1.CARotationTargetAggregator})
	if err != nil {
		t.Fatal(err)
	}

	m := testMachine("w", roleWorker, nil)

	_, plain, err := Render(cluster, m, old, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, stage := range []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping,
	} {
		_, got, err := Render(cluster, m, old, nil, &SystemInputs{CARotation: &CARotation{Stage: stage, New: next}})
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}

		if got != plain {
			t.Errorf("%s: a worker's configuration changed", stage)
		}
	}
}

// TestCARotationAllFourLastStagesEqualTheFinalConfiguration is the no-drift property of
// TestCARotationLastStagesEqualTheFinalConfiguration with all four CAs rotated.
func TestCARotationAllFourLastStagesEqualTheFinalConfiguration(t *testing.T) {
	cluster := testCluster("true")
	old := testBundle(t)

	next, err := secretsbundle.NewRotationFor(time.Now(), testContract(t), []string{
		ichorv1alpha1.CARotationTargetTalos, ichorv1alpha1.CARotationTargetKubernetes,
		ichorv1alpha1.CARotationTargetEtcd, ichorv1alpha1.CARotationTargetAggregator,
	})
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
	}
}

// TestCARotationAggregatorNeedsTheMultiDocumentContract: before v1.14 the aggregator CA is a single
// field with no accepted list, so there is no stage in which both are trusted.
func TestCARotationAggregatorNeedsTheMultiDocumentContract(t *testing.T) {
	cluster := testCluster("true")
	cluster.Spec.VersionContract = contractV113

	next, err := secretsbundle.NewRotationFor(time.Now(), testContract(t), aggregatorOnly)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = Render(cluster, testMachine("cp", roleControlPlane, nil), testBundle(t), nil,
		&SystemInputs{CARotation: &CARotation{Stage: ichorv1alpha1.CARotationStageAccepting, New: next}})
	if err == nil || !strings.Contains(err.Error(), "accepted list") {
		t.Fatalf("an aggregator rotation on a contract older than v1.14 must be refused for that reason, got %v", err)
	}
}
