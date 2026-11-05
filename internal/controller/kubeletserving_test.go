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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	certv1 "k8s.io/api/certificates/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	servingNode = "node-a"
	servingUser = "system:node:node-a"
	servingIP   = "10.0.0.10"

	authenticatedGroup = "system:authenticated"
	adminUser          = "kubernetes-admin"
	denySubject        = "exactly CN=system:node"
	denyUsages         = "key usages"
	mastersGroup       = "system:masters"
)

// fataler is what the fixtures need of *testing.T, so Ginkgo specs can use them too.
type fataler interface {
	Helper()
	Fatal(args ...any)
}

// servingCSR describes one kubelet serving request; the zero value of a field is the good value.
type servingCSR struct {
	cn         string
	orgs       []string
	extraName  []pkix.AttributeTypeAndValue
	dns        []string
	ips        []string
	emails     []string
	uris       []string
	extensions []pkix.Extension
	username   string
	groups     []string
	usages     []certv1.KeyUsage
	signer     string
	rawRequest []byte
}

func (s servingCSR) build(t fataler) *certv1.CertificateSigningRequest {
	t.Helper()

	def := func(v, d string) string {
		if v == "" {
			return d
		}

		return v
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.CertificateRequest{
		Subject:         pkix.Name{CommonName: def(s.cn, servingUser), Organization: s.orgs, ExtraNames: s.extraName},
		DNSNames:        s.dns,
		EmailAddresses:  s.emails,
		ExtraExtensions: s.extensions,
	}
	if tmpl.Subject.Organization == nil {
		tmpl.Subject.Organization = []string{nodesGroup}
	}

	if s.dns == nil && s.ips == nil && s.emails == nil && s.uris == nil {
		tmpl.DNSNames = []string{servingNode}
		tmpl.IPAddresses = []net.IP{net.ParseIP(servingIP)}
	}

	for _, ip := range s.ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}

	for _, u := range s.uris {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}

		tmpl.URIs = append(tmpl.URIs, parsed)
	}

	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}

	request := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if s.rawRequest != nil {
		request = s.rawRequest
	}

	usages := s.usages
	if usages == nil {
		usages = []certv1.KeyUsage{certv1.UsageDigitalSignature, certv1.UsageServerAuth}
	}

	groups := s.groups
	if groups == nil {
		groups = []string{nodesGroup, authenticatedGroup}
	}

	return &certv1.CertificateSigningRequest{
		Name: "csr-test",
		Spec: certv1.CertificateSigningRequestSpec{
			Request: request, SignerName: def(s.signer, kubeletServingSigner), Usages: usages,
			Username: def(s.username, servingUser), Groups: groups,
		},
	}
}

func servingIdentityOf(addrs ...string) ServingIdentity {
	id := ServingIdentity{Names: []string{servingNode, "node-a.example.com"}}
	for _, a := range addrs {
		id.Addresses = append(id.Addresses, netip.MustParseAddr(a))
	}

	return id
}

// TestJudgeKubeletServingRequest. Only a request from the node's own kubelet that names
// only the node's own names and addresses and asks for the two permitted usage sets is approved;
// every deviation is denied with a reason that names what was wrong.
func TestJudgeKubeletServingRequest(t *testing.T) {
	sanExt := func() pkix.Extension {
		return pkix.Extension{Id: asn1.ObjectIdentifier{2, 5, 29, 15}, Value: []byte{3, 2, 5, 160}} // key usage
	}

	cases := []struct {
		name string
		csr  servingCSR
		// deny is a substring of the reason; "" means the request is approved.
		deny string
	}{
		{name: "the node's own request", csr: servingCSR{}},
		{
			name: "with key encipherment, which the signer also permits",
			csr:  servingCSR{usages: []certv1.KeyUsage{certv1.UsageDigitalSignature, certv1.UsageKeyEncipherment, certv1.UsageServerAuth}},
		},
		{name: "an FQDN the node reports", csr: servingCSR{dns: []string{"node-a.example.com"}, ips: []string{servingIP}}},
		{name: "another user's request", csr: servingCSR{username: "system:node:node-b"}, deny: "is not the node"},
		{name: "an admin's request", csr: servingCSR{username: adminUser}, deny: "is not the node"},
		{name: "the right user outside system:nodes", csr: servingCSR{groups: []string{authenticatedGroup}}, deny: "system:nodes"},
		{name: "a subject that is not a node", csr: servingCSR{cn: adminUser, username: adminUser}, deny: "system:node:<name>"},
		{name: "another organization", csr: servingCSR{orgs: []string{mastersGroup}}, deny: denySubject},
		{name: "two organizations", csr: servingCSR{orgs: []string{nodesGroup, mastersGroup}}, deny: denySubject},
		{
			name: "an extra subject attribute",
			csr: servingCSR{extraName: []pkix.AttributeTypeAndValue{
				{Type: asn1.ObjectIdentifier{2, 5, 4, 11}, Value: "ops"}}},
			deny: denySubject,
		},
		{name: "a DNS name the node does not own", csr: servingCSR{dns: []string{"kubernetes.default.svc"}, ips: []string{servingIP}}, deny: "kubernetes.default.svc"},
		{name: "an IP address the node does not own", csr: servingCSR{dns: []string{servingNode}, ips: []string{"10.0.0.99"}}, deny: "10.0.0.99"},
		{name: "the address of another node", csr: servingCSR{ips: []string{"10.0.0.11"}}, deny: "10.0.0.11"},
		{name: "an email address", csr: servingCSR{dns: []string{servingNode}, emails: []string{"a@example.com"}}, deny: "email address or URI"},
		{name: "a URI", csr: servingCSR{dns: []string{servingNode}, uris: []string{"spiffe://example.com/x"}}, deny: "email address or URI"},
		{name: "no names at all", csr: servingCSR{emails: []string{}, dns: []string{}}, deny: "no DNS name or IP"},
		{name: "client auth added", csr: servingCSR{usages: []certv1.KeyUsage{certv1.UsageDigitalSignature, certv1.UsageServerAuth, certv1.UsageClientAuth}}, deny: denyUsages},
		{name: "server auth only", csr: servingCSR{usages: []certv1.KeyUsage{certv1.UsageServerAuth}}, deny: denyUsages},
		{name: "no server auth", csr: servingCSR{usages: []certv1.KeyUsage{certv1.UsageDigitalSignature, certv1.UsageKeyEncipherment}}, deny: denyUsages},
		{name: "a duplicated usage", csr: servingCSR{usages: []certv1.KeyUsage{certv1.UsageDigitalSignature, certv1.UsageServerAuth, certv1.UsageServerAuth}}, deny: denyUsages},
		{name: "an extension besides the names", csr: servingCSR{extensions: []pkix.Extension{sanExt()}}, deny: "extension"},
		{name: "garbage", csr: servingCSR{rawRequest: []byte("not a request")}, deny: "PEM"},
		{name: "a request that is not PKCS#10", csr: servingCSR{rawRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{1, 2, 3}})}, deny: "PKCS#10"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			csr := tc.csr.build(t)

			parsed, reason := parseServingRequest(csr)
			if reason == "" {
				reason = checkServingIdentity(parsed, servingIdentityOf(servingIP))
			}

			if tc.deny == "" {
				if reason != "" {
					t.Fatalf("denied: %s", reason)
				}

				if parsed.nodeName != servingNode {
					t.Fatalf("node name = %q", parsed.nodeName)
				}

				return
			}

			if reason == "" {
				t.Fatalf("approved, want a denial naming %q", tc.deny)
			}

			if !strings.Contains(reason, tc.deny) {
				t.Fatalf("reason %q does not mention %q", reason, tc.deny)
			}
		})
	}

	t.Run("a tampered request fails its signature", func(t *testing.T) {
		csr := servingCSR{}.build(t)
		block, _ := pem.Decode(csr.Spec.Request)
		block.Bytes[len(block.Bytes)-3] ^= 0xff
		csr.Spec.Request = pem.EncodeToMemory(block)

		if _, denial := parseServingRequest(csr); denial == "" {
			t.Fatal("approved")
		}
	})
}

func TestIsOpenServingCSR(t *testing.T) {
	open := servingCSR{}.build(t)
	if !isOpenServingCSR(open) {
		t.Fatal("a fresh request is open")
	}

	other := servingCSR{signer: certv1.KubeAPIServerClientKubeletSignerName}.build(t)
	if isOpenServingCSR(other) {
		t.Error("another signer's request must never be touched")
	}

	for _, ct := range []certv1.RequestConditionType{certv1.CertificateApproved, certv1.CertificateDenied, certv1.CertificateFailed} {
		c := servingCSR{}.build(t)
		c.Status.Conditions = []certv1.CertificateSigningRequestCondition{{Type: ct, Status: "True"}}

		if isOpenServingCSR(c) {
			t.Errorf("a request already %s is not open", ct)
		}
	}
}

func TestValidServingUsages(t *testing.T) {
	ds, sa, ke := certv1.UsageDigitalSignature, certv1.UsageServerAuth, certv1.UsageKeyEncipherment

	for usages, want := range map[string]bool{
		"ds,sa": true, "ds,ke,sa": true, "sa,ds": true,
		"ds": false, "sa": false, "ds,sa,ke,ke": false, "": false, "ke,sa": false,
	} {
		var u []certv1.KeyUsage

		for s := range strings.SplitSeq(usages, ",") {
			switch s {
			case "ds":
				u = append(u, ds)
			case "sa":
				u = append(u, sa)
			case "ke":
				u = append(u, ke)
			}
		}

		if got := validServingUsages(u); got != want {
			t.Errorf("%q: %v, want %v", usages, got, want)
		}
	}
}

func servingCert(t fataler, notBefore, notAfter metav1.Time) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: servingUser},
		NotBefore: notBefore.Time, NotAfter: notAfter.Time,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: pemTypeCertificate, Bytes: der})
}

// TestIssuedBy: only a certificate on a request Ichor itself approved is recorded for the node.
func TestIssuedBy(t *testing.T) {
	now := metav1.Now()
	csr := servingCSR{}.build(t)
	csr.Status.Certificate = servingCert(t, now, metav1.NewTime(now.Add(24*time.Hour)))
	csr.Status.Conditions = []certv1.CertificateSigningRequestCondition{
		{Type: certv1.CertificateApproved, Status: "True", Reason: reasonKubeletServingApproved},
	}

	st := issuedBy(csr, servingNode)
	if st == nil || st.Request != "csr-test" || !st.NotAfter.After(now.Time) {
		t.Fatalf("issuedBy = %+v", st)
	}

	if issuedBy(csr, "node-b") != nil {
		t.Error("another node's request counted")
	}

	byHand := csr.DeepCopy()
	byHand.Status.Conditions[0].Reason = "ApprovedByAdmin"

	if issuedBy(byHand, servingNode) != nil {
		t.Error("a request somebody else approved counted as Ichor's own")
	}
}

func TestServingConditions(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{}

	setServingConditions(cluster, &coverageReport{missing: []string{"cp-1", "w-1"}}, true)

	c := apimeta.FindStatusCondition(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingCertificates)
	if c == nil || c.Status != metav1.ConditionFalse || !strings.Contains(c.Message, "cp-1, w-1") {
		t.Fatalf("certificates condition = %+v", c)
	}

	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified) {
		t.Fatal("the latch is set before every machine has a certificate")
	}

	setServingConditions(cluster, &coverageReport{covered: true}, true)

	if !apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified) {
		t.Fatal("the latch did not set once everything is covered")
	}

	// A machine joins: the certificates condition falls, the latch stays.
	setServingConditions(cluster, &coverageReport{missing: []string{"w-2"}}, true)

	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingCertificates) {
		t.Error("certificates condition stayed True with a machine missing")
	}

	if !apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified) {
		t.Error("a new node took the latch away")
	}

	// Verification switched off lowers the latch.
	setServingConditions(cluster, &coverageReport{covered: true}, false)

	if apimeta.FindStatusCondition(cluster.Status.Conditions, ichorv1alpha1.ConditionKubeletServingVerified) != nil {
		t.Error("the latch outlived verifyFromAPIServer")
	}
}

// TestKubeletServingLatchReachesTheMachines: the latch changes what render emits, so it must move
// the desired-inputs hash (else the change reads as drift) and wake the machines when it flips.
func TestKubeletServingLatchReachesTheMachines(t *testing.T) {
	mk := func(latched bool) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		c.Spec.Security = &ichorv1alpha1.TalosClusterSecurity{KubeletServingCertificates: &ichorv1alpha1.TalosClusterKubeletServing{
			Enabled: true, VerifyFromAPIServer: true,
		}}

		if latched {
			apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
				Type: ichorv1alpha1.ConditionKubeletServingVerified, Status: metav1.ConditionTrue, Reason: "AllNodesIssued",
			})
		}

		return c
	}

	m := &ichorv1alpha1.TalosMachine{}

	before, err := desiredInputsHash(mk(false), m, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	after, err := desiredInputsHash(mk(true), m, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if before == after {
		t.Error("the latch does not move the desired-inputs hash")
	}

	p := clusterStatusRenderInputChanged()

	if !p.Update(event.UpdateEvent{ObjectOld: mk(false), ObjectNew: mk(true)}) {
		t.Error("the latch flipping does not wake the machines")
	}
}
