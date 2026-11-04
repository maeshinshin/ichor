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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

// controllerStub answers a TalosCredentialRequest on creation the way the controller does (ADR 0124): it
// signs the request's public key with the fixture's Talos CA. refuse, when set, answers Failed instead.
func controllerStub(t *testing.T, f *credFixture, refuse string, seen *[]ichorv1alpha1.TalosCredentialRequest) client.Client {
	t.Helper()

	src, err := credentials.NewSources(f.old, nil)
	if err != nil {
		t.Fatal(err)
	}

	wc, ok := f.k8s.(client.WithWatch)
	if !ok {
		t.Fatal("the fake client is not a WithWatch")
	}

	return interceptor.NewClient(wc, interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			req, isReq := obj.(*ichorv1alpha1.TalosCredentialRequest)
			if !isReq {
				return cl.Create(ctx, obj, opts...)
			}

			*seen = append(*seen, *req.DeepCopy())

			if refuse != "" {
				req.Status = ichorv1alpha1.TalosCredentialRequestStatus{Phase: ichorv1alpha1.CredentialPhaseFailed, Message: refuse}

				return cl.Create(ctx, obj, opts...)
			}

			var ttl time.Duration
			if req.Spec.TTL != nil {
				ttl = req.Spec.TTL.Duration
			}

			ttl, err := credentials.PurposeLifetime(req.Spec.Purpose, ttl)
			if err != nil {
				t.Fatal(err)
			}

			roles, err := credentials.TalosRoles(req.Spec.Purpose, req.Spec.TalosRole)
			if err != nil {
				t.Fatal(err)
			}

			crt, notAfter, err := src.IssueTalos([]byte(req.Spec.CSR), roles, ttl)
			if err != nil {
				t.Fatal(err)
			}

			na := metav1.NewTime(notAfter)
			req.Status = ichorv1alpha1.TalosCredentialRequestStatus{
				Phase: ichorv1alpha1.CredentialPhaseIssued, Certificate: string(crt), Trust: string(src.TalosTrust), NotAfter: &na,
			}

			return cl.Create(ctx, obj, opts...)
		},
	})
}

func (f *credFixture) readerRequest() readerTalosconfigRequest {
	return readerTalosconfigRequest{
		Cluster: f.name, Namespace: testNamespace, TalosconfigIn: filepath.Join(f.dir, "talosconfig"),
		Poll: time.Millisecond, Wait: time.Second,
	}
}

// ADR 0124: the file is an os:reader talosconfig for the controller's certificate and a key made here.
func TestReaderTalosconfigIsOSReaderAndSignedForALocalKey(t *testing.T) {
	f := newCredFixture(t, false, "")

	var seen []ichorv1alpha1.TalosCredentialRequest

	req := f.readerRequest()
	req.TTL = 2 * time.Hour

	out, notAfter, err := writeReaderTalosconfig(context.Background(), controllerStub(t, f, "", &seen), req)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(f.dir, "talosconfig") + readerSuffix; out != want {
		t.Errorf("wrote %s, want %s", out, want)
	}

	if st, err := os.Stat(out); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v, want 0600", st, err)
	}

	ca, crtPEM, ctx := readTalosconfigCreds(t, out)
	if !chainsTo(t, crtPEM, ca) {
		t.Error("the certificate does not chain to the trust bundle in the talosconfig")
	}

	block, _ := pem.Decode(crtPEM)
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(crt.Subject.Organization, []string{"os:reader"}) {
		t.Errorf("O = %v, want [os:reader]", crt.Subject.Organization)
	}

	if d := time.Until(crt.NotAfter); d > 2*time.Hour+time.Minute || d < time.Hour+50*time.Minute || !notAfter.Equal(crt.NotAfter) {
		t.Errorf("expires in %s (reported %s), want about 2h", d, notAfter)
	}

	if ctx.Key == "" || !slices.Equal(ctx.Endpoints, []string{credEndpoint}) {
		t.Errorf("key set: %v, endpoints %v, want the control plane %s", ctx.Key != "", ctx.Endpoints, credEndpoint)
	}

	if len(seen) != 1 {
		t.Fatalf("%d requests created, want 1", len(seen))
	}

	r := seen[0]
	if r.Spec.Purpose != ichorv1alpha1.CredentialPurposeTalosconfigReader || r.Spec.CSR == "" || r.Spec.MachineRef != nil ||
		r.Spec.TTL == nil || r.Spec.TTL.Duration != 2*time.Hour || strings.Contains(r.Spec.CSR, "PRIVATE") {
		t.Errorf("unexpected request %+v", r.Spec)
	}
}

func TestReaderTalosconfigDefaultsToTheDefaultLifetime(t *testing.T) {
	f := newCredFixture(t, false, "")

	var seen []ichorv1alpha1.TalosCredentialRequest

	_, notAfter, err := writeReaderTalosconfig(context.Background(), controllerStub(t, f, "", &seen), f.readerRequest())
	if err != nil {
		t.Fatal(err)
	}

	if d := time.Until(notAfter); d > credentials.TalosconfigLifetime+time.Minute || d < credentials.TalosconfigLifetime-time.Minute {
		t.Errorf("default expires in %s, want about %s", d, credentials.TalosconfigLifetime)
	}

	if seen[0].Spec.TTL != nil {
		t.Errorf("no --ttl must leave spec.ttl empty so the controller's default applies, got %v", seen[0].Spec.TTL)
	}
}

func TestReaderTalosconfigRefusesLongerLifetimesBeforeAskingAnyone(t *testing.T) {
	f := newCredFixture(t, false, "")

	var seen []ichorv1alpha1.TalosCredentialRequest

	for _, ttl := range []time.Duration{credentials.MaxTalosconfigLifetime + time.Second, -time.Second} {
		req := f.readerRequest()
		req.TTL = ttl

		if _, _, err := writeReaderTalosconfig(context.Background(), controllerStub(t, f, "", &seen), req); err == nil {
			t.Errorf("--ttl %s was accepted", ttl)
		}
	}

	if len(seen) != 0 {
		t.Errorf("%d requests were created for refused lifetimes", len(seen))
	}
}

func TestReaderTalosconfigWritesNothingWhenTheControllerRefuses(t *testing.T) {
	f := newCredFixture(t, false, "")

	var seen []ichorv1alpha1.TalosCredentialRequest

	_, _, err := writeReaderTalosconfig(context.Background(), controllerStub(t, f, "the cluster's secrets bundle could not be read", &seen), f.readerRequest())
	if err == nil || !strings.Contains(err.Error(), "secrets bundle could not be read") {
		t.Fatalf("err = %v, want the controller's message", err)
	}

	if _, err := os.Stat(filepath.Join(f.dir, "talosconfig") + readerSuffix); !os.IsNotExist(err) {
		t.Errorf("a file was written: %v", err)
	}
}

func TestReaderTalosconfigNeverOverwritesTheCurrentTalosconfig(t *testing.T) {
	f := newCredFixture(t, false, "")

	current := filepath.Join(f.dir, "talosconfig")
	if err := os.WriteFile(current, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen []ichorv1alpha1.TalosCredentialRequest

	req := f.readerRequest()
	req.Out = current

	if _, _, err := writeReaderTalosconfig(context.Background(), controllerStub(t, f, "", &seen), req); err == nil {
		t.Fatal("overwriting the current talosconfig was accepted")
	}

	if got, _ := os.ReadFile(current); string(got) != "mine" {
		t.Errorf("the current talosconfig changed: %q", got)
	}

	if len(seen) != 0 {
		t.Error("a request was created before the output was checked")
	}
}

func TestReaderTalosconfigNeedsAnEndpoint(t *testing.T) {
	f := newCredFixture(t, false, "")

	var seen []ichorv1alpha1.TalosCredentialRequest

	req := f.readerRequest()
	req.Cluster = "other"

	if _, _, err := writeReaderTalosconfig(context.Background(), controllerStub(t, f, "", &seen), req); err == nil {
		t.Error("a missing cluster was accepted")
	}
}
