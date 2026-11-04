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

package secretsbundle

import (
	"bytes"
	"strings"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

func TestRotationForEtcdAndAggregatorOnly(t *testing.T) {
	bundle := newTestBundle(t)

	contract, _ := coreconfig.ParseContractFromVersion("v1.14")

	rot, err := NewRotationFor(time.Now(), contract, []string{"kubernetes-aggregator", "etcd"})
	if err != nil {
		t.Fatal(err)
	}

	if rot.Talos != nil || rot.Kubernetes != nil {
		t.Fatal("a CA that is not a target must not be generated")
	}

	if got := strings.Join(rot.Targets(), ","); got != "etcd,kubernetes-aggregator" {
		t.Fatalf("targets are listed in a fixed order, got %q", got)
	}

	if rot.RotatesCredentials() {
		t.Fatal("etcd and the aggregator CA change nothing the operator holds")
	}

	raw, err := rot.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	back, err := ParseRotation(raw)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(back.Etcd.Key, rot.Etcd.Key) || !bytes.Equal(back.Aggregator.Key, rot.Aggregator.Key) {
		t.Fatal("the new CA keys did not survive the round trip")
	}

	applied := rot.Apply(bundle)
	if !bytes.Equal(applied.Certs.Etcd.Crt, rot.Etcd.Crt) || !bytes.Equal(applied.Certs.K8sAggregator.Crt, rot.Aggregator.Crt) {
		t.Fatal("Apply did not replace the etcd and aggregator CAs")
	}

	if applied.Certs.OS != bundle.Certs.OS || applied.Certs.K8s != bundle.Certs.K8s || applied.Cluster != bundle.Cluster {
		t.Fatal("Apply changed something that is not a target")
	}

	before, _ := BundleFingerprint(bundle)
	after, _ := BundleFingerprint(applied)

	if before != after {
		t.Fatal("BundleFingerprint is about the CAs credentials come from and must not move for etcd and the aggregator")
	}

	exportBefore, _ := ExportFingerprint(bundle, nil)
	exportAfter, _ := ExportFingerprint(applied, nil)

	if exportBefore == "" || exportBefore == exportAfter {
		t.Fatalf("a saved copy with the old etcd CA is stale: %q %q", exportBefore, exportAfter)
	}
}

func TestRotationTalosAndKubernetesStayTheDefault(t *testing.T) {
	contract, _ := coreconfig.ParseContractFromVersion("v1.14")

	rot, err := NewRotation(time.Now(), contract)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(rot.Targets(), ","); got != "talos,kubernetes" || !rot.RotatesCredentials() {
		t.Fatalf("a rotation without a choice replaces the Talos and Kubernetes CAs, got %q", got)
	}

	if rot.Etcd != nil || rot.Aggregator != nil {
		t.Fatal("the default rotation must not generate the etcd or aggregator CA")
	}
}

func TestRotationValidateNeedsATargetAndCompleteCAs(t *testing.T) {
	if (&Rotation{}).Validate() == nil {
		t.Fatal("a rotation without any CA must be refused")
	}

	contract, _ := coreconfig.ParseContractFromVersion("v1.14")

	rot, _ := NewRotationFor(time.Now(), contract, []string{"etcd"})
	rot.Etcd.Key = nil

	if rot.Validate() == nil {
		t.Fatal("a CA without its key must be refused")
	}

	if _, err := NewRotationFor(time.Now(), contract, []string{"nonsense"}); err == nil {
		t.Fatal("an unknown target must be refused")
	}

	if _, err := NewRotationFor(time.Now(), contract, nil); err == nil {
		t.Fatal("no target must be refused")
	}
}
