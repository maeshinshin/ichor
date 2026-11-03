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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	verifyServiceName = "ichor-ichor-ui-service.ichor-system.svc"
	verifyLeafCN      = "ichor-ui" // the subject the controller gives its leaf
)

// ichorLeafServer is an HTTPS server whose certificate is an Ichor-style leaf for verifyServiceName,
// signed by a private CA. It returns the server and the CA PEM the ichor-ui-ca ConfigMap would hold.
func ichorLeafServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ichor-oidc-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	leafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: verifyLeafCN},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{verifyServiceName}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return srv, pem.EncodeToMemory(&pem.Block{Type: testPEMCert, Bytes: caDER})
}

// The in-cluster path: the Service name verifies against the CA published in ichor-ui-ca, and without
// that CA the failure is the verification one (the only failure that falls back with a warning).
func TestTunnelClientVerifiesTheIchorLeafByServiceName(t *testing.T) {
	srv, caPEM := ichorLeafServer(t)

	_, base, err := tunnelClient(context.Background(), portOf(srv), "", caPEM, verifyServiceName)
	if err != nil || !strings.HasPrefix(base, "https://") {
		t.Fatalf("base = %s, err = %v; want https", base, err)
	}

	_, _, err = tunnelClient(context.Background(), portOf(srv), "", nil, verifyServiceName)
	if !errors.Is(err, errTLSVerify) {
		t.Fatalf("without the CA: err = %v; want errTLSVerify", err)
	}
}

// uiServiceForVerify is the ichor-ui Service of an install that serves HTTPS on 8443 and has ichor-ui-ca.
func uiServiceForVerify() []client.Object {
	sel := uiTestSelector

	return []client.Object{
		&corev1.Service{
			Name: testUIServiceName, Namespace: ichorControllerNamespace, Labels: map[string]string{uiComponentLabel: uiComponentValue},
			Spec: corev1.ServiceSpec{Selector: sel, Ports: []corev1.ServicePort{{Port: uiHTTPSPort}}},
		},
		&corev1.Pod{
			Name: "ichor-ui-0", Namespace: ichorControllerNamespace, Labels: sel,
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	}
}

// connectAPI falls back, with its warning, only when ichor-ui cannot be verified or reached. A verified
// ichor-ui is used with no warning.
func TestConnectAPIUsesVerifiedIchorUIWithoutWarning(t *testing.T) {
	srv, caPEM := ichorLeafServer(t)

	cases := []struct {
		name      string
		configMap bool
		wantLocal bool
		wantWarn  bool
	}{
		{"verifies against the published CA", true, false, false},
		{"no CA to verify with falls back with the warning", false, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := uiServiceForVerify()
			if tc.configMap {
				objs = append(objs, &corev1.ConfigMap{
					Name: uiCAConfigMap, Namespace: ichorControllerNamespace,
					Data: map[string]string{uiCAKey: string(caPEM)},
				})
			}

			stderr := &bytes.Buffer{}
			deps := apiDeps{
				K8s: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build(),
				Forward: func(ctx context.Context, _ *corev1.Pod, _, _ int32, ready chan struct{}) error {
					close(ready)
					<-ctx.Done()

					return nil
				},
				FreePort: func() (int32, error) { return portOf(srv), nil },
				Stderr:   stderr,
			}

			c, err := connectAPI(context.Background(), apiOptions{IchorNamespace: ichorControllerNamespace}, deps, false)
			if err != nil {
				t.Fatal(err)
			}

			defer c.Close()

			if c.Local != tc.wantLocal {
				t.Fatalf("Local = %v, want %v", c.Local, tc.wantLocal)
			}

			if warned := strings.Contains(stderr.String(), "ichor-ui"); warned != tc.wantWarn {
				t.Fatalf("warning = %q, want a warning %v", stderr.String(), tc.wantWarn)
			}
		})
	}
}
