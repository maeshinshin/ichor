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
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const (
	testTLSHost       = "ichor.example.com"
	testPEMCert       = "CERTIFICATE"
	testTLSServerName = "kubernetes"
)

// writeTestKeyPair writes a self-signed certificate and key under t.TempDir().
func writeTestKeyPair(t *testing.T) (certFile, keyFile string, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: testTLSHost},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{testTLSHost},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: testPEMCert, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	return certFile, keyFile, keyPEM
}

func tlsSecret(name string, typ corev1.SecretType) *corev1.Secret {
	s := &corev1.Secret{Type: typ}
	s.Namespace, s.Name = ichorControllerNamespace, name

	return s
}

func getUIDeployment(t *testing.T, k client.Client) appsv1.Deployment {
	t.Helper()

	var dep appsv1.Deployment
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	return dep
}

func assertUITLS(t *testing.T, dep appsv1.Deployment, secret string) {
	t.Helper()

	c := dep.Spec.Template.Spec.Containers[0]
	if !slices.Contains(c.Args, "--tls-cert-file=/etc/ichor/tls/tls.crt") || !slices.Contains(c.Args, "--tls-key-file=/etc/ichor/tls/tls.key") {
		t.Errorf("TLS flags missing: %v", c.Args)
	}

	if !slices.ContainsFunc(dep.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == "tls" && v.Secret != nil && v.Secret.SecretName == secret }) {
		t.Errorf("tls volume for %s missing: %+v", secret, dep.Spec.Template.Spec.Volumes)
	}

	if !slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool {
		return m.Name == "tls" && m.MountPath == "/etc/ichor/tls" && m.ReadOnly
	}) {
		t.Errorf("tls mount missing: %+v", c.VolumeMounts)
	}

	if p := c.ReadinessProbe; p == nil || p.HTTPGet == nil || p.HTTPGet.Scheme != corev1.URISchemeHTTPS || p.HTTPGet.Port.IntValue() != 8080 || p.HTTPGet.Path != "/auth/config" {
		t.Errorf("readiness is not an HTTPS probe on 8080: %+v", p)
	}

	if p := c.LivenessProbe; p == nil || p.HTTPGet != nil || p.TCPSocket == nil || p.TCPSocket.Port.IntValue() != 8080 {
		t.Errorf("liveness is not a tcpSocket probe on 8080: %+v", p)
	}
}

func TestRunInit_UITLSSecretAndKubeAPIFlagsEditTheDeployment(t *testing.T) {
	withTempHome(t)

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).WithObjects(tlsSecret("my-cert", corev1.SecretTypeTLS)).Build()
	deps, _, _ := newFakeInitDeps(t, k)

	var stderr bytes.Buffer

	deps.Stderr = &stderr

	opts := validInitOptions()
	opts.UIURL, opts.UITLSSecret = testUIURL, "my-cert"
	opts.AllowUnauthenticated = false
	opts.KubeAPIServerURL, opts.KubeAPITLSServerName = "https://k8s.example.com:443", testTLSServerName

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	dep := getUIDeployment(t, k)
	assertUITLS(t, dep, "my-cert")

	args := dep.Spec.Template.Spec.Containers[0].Args
	for _, want := range []string{"--kube-api-server-url=https://k8s.example.com:443", "--kube-api-tls-server-name=" + testTLSServerName} {
		if !slices.Contains(args, want) {
			t.Errorf("args lack %s: %v", want, args)
		}
	}

	var sec corev1.Secret
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: uiTLSStoredSecretName}, &sec); !apierrors.IsNotFound(err) {
		t.Errorf("init stored a Secret although --ui-tls-secret named one: %v", err)
	}
}

func TestRunInit_UITLSCertAndKeyAreStoredAsASecretAndNeverPrinted(t *testing.T) {
	withTempHome(t)

	certFile, keyFile, keyPEM := writeTestKeyPair(t)
	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).Build()
	deps, _, _ := newFakeInitDeps(t, k)

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	opts := validInitOptions()
	opts.UIURL, opts.UITLSCert, opts.UITLSKey = testUIURL, certFile, keyFile
	opts.AllowUnauthenticated = false

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	assertUITLS(t, getUIDeployment(t, k), uiTLSStoredSecretName)

	var sec corev1.Secret
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: uiTLSStoredSecretName}, &sec); err != nil {
		t.Fatal(err)
	}

	if sec.Type != corev1.SecretTypeTLS || !bytes.Equal(sec.Data[corev1.TLSPrivateKeyKey], keyPEM) || len(sec.Data[corev1.TLSCertKey]) == 0 {
		t.Errorf("stored Secret = type %s, keys %d", sec.Type, len(sec.Data))
	}

	for _, out := range []string{stdout.String(), stderr.String()} {
		if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, string(keyPEM)) {
			t.Error("key material was printed")
		}
	}
}

func TestRunInit_UIExtrasAreCheckedBeforeAnythingHappens(t *testing.T) {
	certFile, keyFile, _ := writeTestKeyPair(t)
	badFile := filepath.Join(t.TempDir(), "bad.pem")

	if err := os.WriteFile(badFile, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*initOptions){
		"tls secret with no sign-in":   func(o *initOptions) { o.UITLSSecret, o.AllowUnauthenticated = "x", true },
		"kube api url with no sign-in": func(o *initOptions) { o.KubeAPIServerURL, o.AllowUnauthenticated = "https://k8s.example.com", true },
		"secret and files": func(o *initOptions) {
			o.UIURL, o.UITLSSecret, o.UITLSCert, o.UITLSKey = testUIURL, "x", certFile, keyFile
		},
		"cert without key":             func(o *initOptions) { o.UIURL, o.UITLSCert = testUIURL, certFile },
		"key without cert":             func(o *initOptions) { o.UIURL, o.UITLSKey = testUIURL, keyFile },
		"unreadable cert":              func(o *initOptions) { o.UIURL, o.UITLSCert, o.UITLSKey = testUIURL, "/nonexistent/c.pem", keyFile },
		"garbage pair":                 func(o *initOptions) { o.UIURL, o.UITLSCert, o.UITLSKey = testUIURL, badFile, badFile },
		"bad secret name":              func(o *initOptions) { o.UIURL, o.UITLSSecret = testUIURL, "Not A Name" },
		"http kube api url":            func(o *initOptions) { o.UIURL, o.KubeAPIServerURL = testUIURL, "http://k8s.example.com" },
		"kube api url with a query":    func(o *initOptions) { o.UIURL, o.KubeAPIServerURL = testUIURL, "https://k8s.example.com/?a=b" },
		"bad kube api tls server name": func(o *initOptions) { o.UIURL, o.KubeAPITLSServerName = testUIURL, "a b/c" },
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)

			k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).Build()
			deps, maint, _ := newFakeInitDeps(t, k)

			opts := validInitOptions()
			opts.AllowUnauthenticated = false
			mutate(&opts)

			if err := runInit(context.Background(), opts, deps); err == nil {
				t.Fatal("expected an error")
			}

			if maint.callCount != 0 {
				t.Error("the node was configured although the flags were invalid")
			}

			assertNothingWritten(t, k)
		})
	}
}

func TestRunInit_UITLSSecretMustExistAndBeOfTypeTLS(t *testing.T) {
	for name, extra := range map[string][]client.Object{
		"missing":    nil,
		"wrong type": {tlsSecret("my-cert", corev1.SecretTypeOpaque)},
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)

			k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).WithObjects(extra...).Build()
			deps, _, _ := newFakeInitDeps(t, k)

			opts := validInitOptions()
			opts.UIURL, opts.UITLSSecret = testUIURL, "my-cert"
			opts.AllowUnauthenticated = false

			if err := runInit(context.Background(), opts, deps); err == nil {
				t.Fatal("expected an error")
			}

			assertNothingWritten(t, k)
		})
	}
}

// assertNothingWritten: no admin user, no credentials Secret, no stored TLS Secret, and an untouched ichor-ui Deployment.
func assertNothingWritten(t *testing.T, k client.Client) {
	t.Helper()

	ctx := context.Background()

	var users ichorv1alpha1.IchorUserList
	if err := k.List(ctx, &users); err != nil || len(users.Items) != 0 {
		t.Errorf("an admin user was created: %v %v", err, users.Items)
	}

	for _, name := range []string{ichorauth.CredentialsSecretName, uiTLSStoredSecretName} {
		var sec corev1.Secret
		if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: name}, &sec); !apierrors.IsNotFound(err) {
			t.Errorf("Secret %s was written: %v", name, err)
		}
	}

	if args := getUIDeployment(t, k).Spec.Template.Spec.Containers[0].Args; !slices.Equal(args, []string{"--addr=:8080"}) {
		t.Errorf("ichor-ui args changed: %v", args)
	}
}
