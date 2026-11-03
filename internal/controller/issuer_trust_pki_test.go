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
	"net"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/maeshinshin/ichor/internal/oidctrust"
)

var testPlan = rotationPlan{
	RotateEvery: 30 * 24 * time.Hour, ActivationDelay: 10 * time.Minute,
	TokenLife: 12 * time.Hour, RetireGrace: 2 * time.Hour,
}

func TestRotateKeysTimeline(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// First call: one key that signs at once.
	ks, changed, next, err := rotateKeys(oidctrust.Keys{}, t0, testPlan)
	if err != nil || !changed || len(ks.Keys) != 1 {
		t.Fatalf("first key: changed=%v keys=%d err=%v", changed, len(ks.Keys), err)
	}

	first := ks.Keys[0]

	if s, ok := ks.Signing(t0); !ok || s.KID != first.KID {
		t.Fatal("the first key must sign immediately")
	}

	if want := t0.Add(testPlan.RotateEvery); !next.Equal(want) {
		t.Fatalf("next event = %v, want %v", next, want)
	}

	// Nothing is due before 30 days.
	again, changed, _, err := rotateKeys(ks, t0.Add(29*24*time.Hour), testPlan)
	if err != nil || changed || len(again.Keys) != 1 {
		t.Fatalf("early pass changed the set: changed=%v keys=%d err=%v", changed, len(again.Keys), err)
	}

	// At 30 days a successor is published but does not sign yet.
	t1 := t0.Add(testPlan.RotateEvery)

	ks, changed, next, err = rotateKeys(ks, t1, testPlan)
	if err != nil || !changed || len(ks.Keys) != 2 {
		t.Fatalf("rotation: changed=%v keys=%d err=%v", changed, len(ks.Keys), err)
	}

	second := ks.Keys[1]

	if s, _ := ks.Signing(t1); s.KID != first.KID {
		t.Fatal("the successor must not sign during its publication delay")
	}

	if len(ks.Published(t1)) != 2 {
		t.Fatal("both keys must be published during the delay")
	}

	if want := t1.Add(testPlan.ActivationDelay); !next.Equal(want) {
		t.Fatalf("next event = %v, want the activation %v", next, want)
	}

	// Ten minutes later the successor signs; the old key stays for tokens already issued.
	t2 := t1.Add(testPlan.ActivationDelay)

	if s, _ := ks.Signing(t2); s.KID != second.KID {
		t.Fatal("the successor must sign after the delay")
	}

	ks, changed, _, err = rotateKeys(ks, t2, testPlan)
	if err != nil || changed || len(ks.Keys) != 2 {
		t.Fatalf("activation must not rewrite the set: changed=%v keys=%d err=%v", changed, len(ks.Keys), err)
	}

	// The old key goes TokenLife + grace after the successor started signing, not before.
	retire := t2.Add(testPlan.TokenLife + testPlan.RetireGrace)

	ks, changed, _, err = rotateKeys(ks, retire.Add(-time.Second), testPlan)
	if err != nil || changed || len(ks.Keys) != 2 {
		t.Fatalf("old key dropped early: changed=%v keys=%d err=%v", changed, len(ks.Keys), err)
	}

	ks, changed, _, err = rotateKeys(ks, retire, testPlan)
	if err != nil || !changed || len(ks.Keys) != 1 || ks.Keys[0].KID != second.KID {
		t.Fatalf("old key kept: changed=%v keys=%d err=%v", changed, len(ks.Keys), err)
	}
}

func TestParseKeysRefusesDamage(t *testing.T) {
	if _, err := oidctrust.ParseKeys([]byte("not json")); err == nil {
		t.Fatal("garbage accepted")
	}

	ks, _, _, err := rotateKeys(oidctrust.Keys{}, time.Now(), testPlan)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := ks.Marshal()

	back, err := oidctrust.ParseKeys(raw)
	if err != nil || len(back.Keys) != 1 || back.Keys[0].KID != ks.Keys[0].KID {
		t.Fatalf("round trip: %v", err)
	}

	ks.Keys[0].PEM = "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	raw, _ = ks.Marshal()

	_, err = oidctrust.ParseKeys(raw)
	if err == nil {
		t.Fatal("a key that does not parse was accepted")
	}

	if got := err.Error(); len(got) > 200 {
		t.Fatalf("error is suspiciously long: %q", got)
	}
}

func TestLeafLifecycle(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	ca, err := newCA(now)
	if err != nil {
		t.Fatal(err)
	}

	caCert, caKey, ok := caUsable(ca, now)
	if !ok {
		t.Fatal("fresh CA unusable")
	}

	if _, _, ok := caUsable(ca, now.Add(issuerCALifetime-issuerCARenewBefore+time.Hour)); ok {
		t.Fatal("a CA near its end must be replaced")
	}

	spec := leafSpec{ips: []net.IP{net.ParseIP("10.96.0.10")}, dns: []string{"ichor-ui-service.ichor-system.svc"}}

	leaf, err := newLeaf(caCert, caKey, spec, now)
	if err != nil {
		t.Fatal(err)
	}

	if !leafUsable(leaf, caCert, spec, now) {
		t.Fatal("fresh leaf unusable")
	}

	other := leafSpec{ips: []net.IP{net.ParseIP("10.96.0.11")}, dns: spec.dns}
	if leafUsable(leaf, caCert, other, now) {
		t.Fatal("a leaf for another ClusterIP must be reissued")
	}

	if !leafUsable(leaf, caCert, spec, now.Add(issuerLeafLifetime*2/3-time.Hour)) {
		t.Fatal("leaf renewed too early")
	}

	if leafUsable(leaf, caCert, spec, now.Add(issuerLeafLifetime*2/3+time.Hour)) {
		t.Fatal("leaf not renewed at two thirds of its life")
	}

	otherCA, err := newCA(now)
	if err != nil {
		t.Fatal(err)
	}

	otherCert, _, _ := caUsable(otherCA, now)
	if leafUsable(leaf, otherCert, spec, now) {
		t.Fatal("a leaf from another CA must be reissued")
	}
}

// The serving certificate names the Service in every form a client inside the cluster dials it by,
// the CLI's <name>.<namespace>.svc first, and the external host (docs/adr/0105 decision 2).
func TestLeafSpecNamesTheServiceForEveryClusterDNSForm(t *testing.T) {
	const name = "ichor-ichor-ui-service"

	r := &IssuerTrustReconciler{Namespace: trustNamespace, ExternalURL: "https://ui.example.com"}
	svc := &corev1.Service{Name: name, Namespace: trustNamespace}

	spec := r.leafSpec(svc, net.ParseIP("10.96.0.20"))

	base := name + "." + trustNamespace
	wantDNS := []string{name, base, base + ".svc", base + ".svc.cluster.local", "localhost", "ui.example.com"}
	if !slices.Equal(spec.dns, wantDNS) {
		t.Fatalf("dns = %v, want %v", spec.dns, wantDNS)
	}

	if len(spec.ips) != 2 || !spec.ips[0].Equal(net.ParseIP("10.96.0.20")) || !spec.ips[1].Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("ips = %v, want the ClusterIP and loopback", spec.ips)
	}
}
