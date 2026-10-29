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
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestTunnelClientTrustsCAFromConfigMap: a fake HTTPS server whose certificate
// is the CA published in ichor-ui-ca verifies, and the name is still checked.
func TestTunnelClientTrustsCAFromConfigMap(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)

	caPEM := pem.EncodeToMemory(&pem.Block{Type: testPEMCert, Bytes: srv.Certificate().Raw})

	_, base, err := tunnelClient(context.Background(), portOf(srv), "", caPEM, "example.com")
	if err != nil || !strings.HasPrefix(base, "https://") {
		t.Fatalf("base = %s, err = %v, want https", base, err)
	}

	if _, _, err := tunnelClient(context.Background(), portOf(srv), "", caPEM, "other.example"); err == nil {
		t.Fatal("expected a hostname verification error")
	}

	if _, _, err := tunnelClient(context.Background(), portOf(srv), "", nil, "example.com"); err == nil {
		t.Fatal("expected an unknown-authority error without the CA")
	}
}

func TestUIEndpoint(t *testing.T) {
	svc := func(ports ...int32) *corev1.Service {
		s := &corev1.Service{Name: "ui", Namespace: ichorControllerNamespace}
		for _, p := range ports {
			s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: p})
		}

		return s
	}
	ca := &corev1.ConfigMap{
		Name: uiCAConfigMap, Namespace: ichorControllerNamespace,
		Data: map[string]string{uiCAKey: "PEM"},
	}

	cases := []struct {
		name     string
		svc      *corev1.Service
		withCA   bool
		caFile   bool
		wantPort int32
		wantCA   bool
	}{
		{"pre-0105 Service", svc(8080), true, false, 8080, false},
		{"8443 with ichor-ui-ca", svc(8080, 8443), true, false, 8443, true},
		{"8443 only with ichor-ui-ca", svc(8443), true, false, 8443, true},
		{"both ports, no ConfigMap", svc(8080, 8443), false, false, 8080, false},
		{"both ports, no ConfigMap, --ca-file", svc(8080, 8443), false, true, 8443, false},
		{"8443 only, no ConfigMap", svc(8443), false, false, 8443, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(newTestScheme(t))
			if tc.withCA {
				b = b.WithObjects(ca.DeepCopy())
			}

			port, pemData := uiEndpoint(context.Background(), b.Build(), tc.svc, tc.caFile)
			if port != tc.wantPort || (len(pemData) > 0) != tc.wantCA {
				t.Fatalf("port = %d, ca = %q; want %d, ca %v", port, pemData, tc.wantPort, tc.wantCA)
			}
		})
	}
}
