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
	"crypto/x509"
	"encoding/pem"
	"slices"
	"testing"
	"time"
)

// ADR 0108: the files keep the one-year default, and --ttl / --read-only narrow them.
func TestCredentialsLifetimeAndReadOnly(t *testing.T) {
	f := newCredFixture(t, false, "")

	notAfterOf := func(crtPEM []byte) (time.Time, []string) {
		block, _ := pem.Decode(crtPEM)
		if block == nil {
			t.Fatal("no certificate")
		}

		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}

		return c.NotAfter, c.Subject.Organization
	}

	files, err := writeCredentials(context.Background(), f.k8s, f.request())
	if err != nil {
		t.Fatal(err)
	}

	_, tcrt, _ := readTalosconfigCreds(t, files.Talosconfig)
	if na, org := notAfterOf(tcrt); na.Before(time.Now().Add(364*24*time.Hour)) || !slices.Equal(org, []string{"os:admin"}) {
		t.Fatalf("the default must stay a year of os:admin, got %s %v", na, org)
	}

	req := f.request()
	req.TTL, req.ReadOnly = 2*time.Hour, true

	short, err := writeCredentials(context.Background(), f.k8s, req)
	if err != nil {
		t.Fatal(err)
	}

	_, tcrt, _ = readTalosconfigCreds(t, short.Talosconfig)
	if na, org := notAfterOf(tcrt); na.After(time.Now().Add(2*time.Hour+time.Minute)) || !slices.Equal(org, []string{"os:reader"}) {
		t.Fatalf("--ttl 2h --read-only gave %s %v", na, org)
	}

	_, kcrt, _ := readKubeconfigCreds(t, short.Kubeconfig)
	if na, _ := notAfterOf(kcrt); na.After(time.Now().Add(2*time.Hour + time.Minute)) {
		t.Fatalf("--ttl 2h gave a Kubernetes certificate until %s", na)
	}
}
