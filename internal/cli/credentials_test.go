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
	stdx509 "crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	credSecret   = "cred-bundle"
	credOpName   = "rot-1"
	credServer   = "https://10.0.0.1:6443"
	credContext  = "admin@c"
	credEndpoint = "10.0.0.2:50000"
	// credHandoffToken is the hand-off annotation the fixture's rotation (at AwaitingCredentials) expects.
	credHandoffToken = "talos=n1,kubernetes=n2"
)

// credFixture is a cluster, its bundle Secret (with a rotation in progress when rot is set), a
// control-plane machine and a ca-rotation operation in the given stage; plus a "current" kubeconfig
// that trusts only the old Kubernetes CA, as the operator's does before the rotation.
type credFixture struct {
	k8s      client.Client
	old      *secrets.Bundle
	rot      *secretsbundle.Rotation
	dir      string
	kubePath string
	name     string
}

func newCredFixture(t *testing.T, withRotation bool, stage string) *credFixture {
	t.Helper()

	f := &credFixture{old: newTestBundle(t), dir: t.TempDir()}

	raw, err := yaml.Marshal(f.old)
	if err != nil {
		t.Fatal(err)
	}

	data := map[string][]byte{secretsbundle.SecretKey: raw}

	if withRotation {
		contract, err := coreconfig.ParseContractFromVersion(testVersionContract)
		if err != nil {
			t.Fatal(err)
		}

		if f.rot, err = secretsbundle.NewRotation(time.Now(), contract); err != nil {
			t.Fatal(err)
		}

		if data[secretsbundle.RotationKey], err = f.rot.Marshal(); err != nil {
			t.Fatal(err)
		}
	}

	cluster := newTestCluster()
	cluster.Spec.SecretsRef.Name = credSecret
	f.name = cluster.Name

	machine := newTestMachine(cluster.Name)
	machine.Spec.Role = roleControlPlane
	machine.Spec.Endpoint = credEndpoint

	op := &ichorv1alpha1.TalosOperation{Name: credOpName, Namespace: testNamespace}
	op.Spec.ClusterRef.Name, op.Spec.Kind = cluster.Name, ichorv1alpha1.TalosOperationKindCARotation

	if withRotation {
		op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{
			Stage: stage, Certificates: []ichorv1alpha1.CARotationCertificate{
				{Name: "talos", Previous: "p1", Next: "n1"}, {Name: "kubernetes", Previous: "p2", Next: "n2"},
			},
		}
	}

	secret := &corev1.Secret{Name: credSecret, Namespace: testNamespace, Data: data}
	f.k8s = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, secret, machine, op).Build()

	// The operator's current kubeconfig: old CA only, a client certificate from the old CA.
	f.kubePath = filepath.Join(f.dir, "kubeconfig")
	cfg := clientcmdapi.Config{
		CurrentContext: credContext,
		Clusters:       map[string]*clientcmdapi.Cluster{"c": {Server: credServer, CertificateAuthorityData: f.old.Certs.K8s.Crt}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{credContext: {ClientCertificateData: []byte("old"), ClientKeyData: []byte("old")}},
		Contexts:       map[string]*clientcmdapi.Context{credContext: {Cluster: "c", AuthInfo: credContext, Namespace: k8sFactsSystemNamespace}},
	}

	if err := clientcmd.WriteToFile(cfg, f.kubePath); err != nil {
		t.Fatal(err)
	}

	return f
}

func (f *credFixture) request() credentialRequest {
	return credentialRequest{
		Cluster: f.name, Namespace: testNamespace, IchorNamespace: testNamespace,
		KubeconfigIn: f.kubePath, TalosconfigIn: filepath.Join(f.dir, "talosconfig"),
	}
}

// chainsTo reports whether the certificate in crtPEM verifies as a client certificate against the
// CA certificates in rootsPEM.
func chainsTo(t *testing.T, crtPEM, rootsPEM []byte) bool {
	t.Helper()

	block, _ := pem.Decode(crtPEM)
	if block == nil {
		t.Fatal("not a PEM certificate")
	}

	cert, err := stdx509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	pool := stdx509.NewCertPool()
	pool.AppendCertsFromPEM(rootsPEM)

	_, err = cert.Verify(stdx509.VerifyOptions{Roots: pool, KeyUsages: []stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}})

	return err == nil
}

func certCount(pemData []byte) int {
	n := 0

	for {
		var block *pem.Block

		block, pemData = pem.Decode(pemData)
		if block == nil {
			return n
		}

		n++
	}
}

// A current file gets the suffix appended; a file already carrying it (Ichor's, from rotate ca) is
// written back in place, never with the suffix twice and never over the operator's own file.
func TestDefaultCredentialsOutAppendsOrKeepsTheSuffix(t *testing.T) {
	kube, talos := "/home/op/.kube/config", "/home/op/.talos/config"
	for in, want := range map[string]string{
		kube:                      kube + credentialsSuffix,
		kube + credentialsSuffix:  kube + credentialsSuffix,
		talos:                     talos + credentialsSuffix,
		talos + credentialsSuffix: talos + credentialsSuffix,
	} {
		if got := defaultCredentialsOut(in); got != want {
			t.Errorf("defaultCredentialsOut(%q) = %q, want %q", in, got, want)
		}
	}
}

// The guard refuses to replace the operator's current files, and allows rewriting Ichor's own
// suffixed file in place.
func TestOverwritesCurrentFileOnlyForTheOperatorsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "config.ichor-new")
	mine := filepath.Join(dir, "config")

	for _, p := range []string{own, mine} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if overwritesCurrentFile(own, own) {
		t.Error("rewriting Ichor's own suffixed file in place was refused")
	}

	if !overwritesCurrentFile(mine, mine) {
		t.Error("replacing the operator's current kubeconfig was allowed")
	}
}

func readKubeconfigCreds(t *testing.T, path string) (caData, crt []byte, cfg *clientcmdapi.Config) {
	t.Helper()

	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}

	cluster := cfg.Clusters[cfg.Contexts[cfg.CurrentContext].Cluster]
	user := cfg.AuthInfos[cfg.Contexts[cfg.CurrentContext].AuthInfo]

	return cluster.CertificateAuthorityData, user.ClientCertificateData, cfg
}

func readTalosconfigCreds(t *testing.T, path string) (ca, crt []byte, ctx *clientconfig.Context) {
	t.Helper()

	cfg, err := clientconfig.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx = cfg.Contexts[cfg.Context]

	caPEM, err := base64.StdEncoding.DecodeString(ctx.CA)
	if err != nil {
		t.Fatal(err)
	}

	crtPEM, err := base64.StdEncoding.DecodeString(ctx.Crt)
	if err != nil {
		t.Fatal(err)
	}

	return caPEM, crtPEM, ctx
}

func TestCredentialsDuringRotationTrustBothCAsAndAreSignedByTheNewOne(t *testing.T) {
	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)

	files, err := writeCredentials(context.Background(), f.k8s, f.request())
	if err != nil {
		t.Fatal(err)
	}

	if !files.Dual {
		t.Fatal("a rotation is in progress, the files must say they trust two CAs")
	}

	// Next to the current files, with the suffix; the current kubeconfig is untouched.
	if files.Kubeconfig != f.kubePath+credentialsSuffix {
		t.Fatalf("kubeconfig written to %s", files.Kubeconfig)
	}

	if files.Talosconfig != filepath.Join(f.dir, "talosconfig"+credentialsSuffix) {
		t.Fatalf("talosconfig written to %s", files.Talosconfig)
	}

	if runtime.GOOS != osWindows {
		for _, p := range []string{files.Kubeconfig, files.Talosconfig} {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}

			if fi.Mode().Perm() != bundleFilePerm {
				t.Fatalf("%s mode is %o, want %o", p, fi.Mode().Perm(), bundleFilePerm)
			}
		}
	}

	// Kubernetes: the server is copied; the CA data holds the old and the new CA; the client
	// certificate is signed by the new one only, and is cluster-admin.
	caData, crt, cfg := readKubeconfigCreds(t, files.Kubeconfig)
	if cfg.Clusters["c"].Server != credServer || cfg.Contexts[cfg.CurrentContext].Namespace != "kube-system" {
		t.Fatalf("server or context not copied: %+v", cfg.Clusters["c"])
	}

	if certCount(caData) != 2 {
		t.Fatalf("the kubeconfig trusts %d CAs, want the old and the new", certCount(caData))
	}

	for _, ca := range [][]byte{f.old.Certs.K8s.Crt, f.rot.Kubernetes.Crt} {
		if !bytes.Contains(caData, bytes.TrimSpace(ca)) {
			t.Fatal("the kubeconfig's CA data lacks one of the CAs")
		}
	}

	if !chainsTo(t, crt, f.rot.Kubernetes.Crt) {
		t.Fatal("the kubeconfig's client certificate does not verify against the new Kubernetes CA")
	}

	if chainsTo(t, crt, f.old.Certs.K8s.Crt) {
		t.Fatal("the kubeconfig's client certificate must not be signed by the old Kubernetes CA")
	}

	block, _ := pem.Decode(crt)
	cert, _ := stdx509.ParseCertificate(block.Bytes)

	if !slices.Equal(cert.Subject.Organization, []string{credentials.KubeAdminOrganization}) {
		t.Fatalf("client certificate organization %v", cert.Subject.Organization)
	}

	// Talos: both OS CAs trusted, os:admin certificate from the new one, endpoints from the machines.
	ca, tcrt, tctx := readTalosconfigCreds(t, files.Talosconfig)
	if certCount(ca) != 2 {
		t.Fatalf("the talosconfig trusts %d CAs, want 2", certCount(ca))
	}

	if !chainsTo(t, tcrt, f.rot.Talos.Crt) || chainsTo(t, tcrt, f.old.Certs.OS.Crt) {
		t.Fatal("the talosconfig's client certificate must be signed by the new Talos CA only")
	}

	if !slices.Equal(tctx.Endpoints, []string{credEndpoint}) {
		t.Fatalf("endpoints %v", tctx.Endpoints)
	}

	tblock, _ := pem.Decode(tcrt)
	tcert, _ := stdx509.ParseCertificate(tblock.Bytes)

	if !slices.Contains(tcert.Subject.Organization, "os:admin") {
		t.Fatalf("the Talos client certificate is not os:admin: %v", tcert.Subject.Organization)
	}

	// The current kubeconfig is not overwritten.
	cur, err := clientcmd.LoadFromFile(f.kubePath)
	if err != nil || certCount(cur.Clusters["c"].CertificateAuthorityData) != 1 {
		t.Fatalf("the current kubeconfig changed: %v", err)
	}
}

func TestCredentialsOutsideARotationTrustOneCA(t *testing.T) {
	f := newCredFixture(t, false, "")

	files, err := writeCredentials(context.Background(), f.k8s, f.request())
	if err != nil {
		t.Fatal(err)
	}

	if files.Dual {
		t.Fatal("no rotation is in progress")
	}

	caData, crt, _ := readKubeconfigCreds(t, files.Kubeconfig)
	if certCount(caData) != 1 || !chainsTo(t, crt, f.old.Certs.K8s.Crt) {
		t.Fatal("outside a rotation the kubeconfig trusts and is signed by the one Kubernetes CA")
	}
}

func TestCredentialsFromABundleFileWorkOffline(t *testing.T) {
	f := newCredFixture(t, false, "")
	t.Setenv("HOME", t.TempDir())

	path := saveTestBundle(t, "offline", f.old, "")

	req := f.request()
	req.BundleFile = path
	req.TalosEndpoints = []string{"10.9.9.9"}

	// k8s is nil: nothing may be read from the cluster.
	files, err := writeCredentials(context.Background(), nil, req)
	if err != nil {
		t.Fatal(err)
	}

	caData, crt, _ := readKubeconfigCreds(t, files.Kubeconfig)
	if !bytes.Contains(caData, bytes.TrimSpace(f.old.Certs.K8s.Crt)) || !chainsTo(t, crt, f.old.Certs.K8s.Crt) {
		t.Fatal("the offline kubeconfig does not match the bundle's Kubernetes CA")
	}

	_, tcrt, tctx := readTalosconfigCreds(t, files.Talosconfig)
	if !chainsTo(t, tcrt, f.old.Certs.OS.Crt) || !slices.Equal(tctx.Endpoints, []string{"10.9.9.9"}) {
		t.Fatal("the offline talosconfig does not match the bundle's Talos CA or the given endpoint")
	}
}

func TestBundleFromBeforeTheCurrentCAsIsRefusedUnlessForced(t *testing.T) {
	f := newCredFixture(t, false, "")
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	// newTestBundle is shared: a bundle with CAs of its own needs a fresh one.
	stalePath := saveTestBundle(t, "stale", newFreshTestBundle(t), "")

	curPath := saveTestBundle(t, "current", f.old, "")

	var errw bytes.Buffer

	if _, err := checkBundleCurrent(ctx, f.k8s, &errw, stalePath, f.request(), false); err == nil {
		t.Fatal("a bundle from before the current CAs was accepted without --force")
	}

	if !strings.Contains(errw.String(), "the bundle's CAs differ from the secrets bundle Ichor stores for this cluster") {
		t.Fatalf("no warning for a stale bundle, got %q", errw.String())
	}

	errw.Reset()

	forced, err := checkBundleCurrent(ctx, f.k8s, &errw, stalePath, f.request(), true)
	if err != nil || !forced {
		t.Fatalf("--force: stale %v, err %v", forced, err)
	}

	if !strings.Contains(errw.String(), "the bundle's CAs differ from the secrets bundle Ichor stores") {
		t.Fatalf("--force printed no warning, got %q", errw.String())
	}

	errw.Reset()

	if _, err := checkBundleCurrent(ctx, f.k8s, &errw, curPath, f.request(), false); err != nil || errw.Len() != 0 {
		t.Fatalf("the cluster's own bundle was refused or warned about: %v, %q", err, errw.String())
	}

	// No cluster to compare with: the bundle is taken as it is.
	if _, err := checkBundleCurrent(ctx, nil, &errw, stalePath, f.request(), false); err != nil || errw.Len() != 0 {
		t.Fatalf("an unreachable cluster refused the bundle: %v, %q", err, errw.String())
	}
}

func TestBundleWithTheNewCAsIsCurrentDuringARotation(t *testing.T) {
	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAccepting)
	t.Setenv("HOME", t.TempDir())

	withNew := *f.old
	certs := *f.old.Certs
	certs.OS, certs.K8s = f.rot.Talos, f.rot.Kubernetes
	withNew.Certs = &certs

	path := saveTestBundle(t, "new", &withNew, "")

	var errw bytes.Buffer
	if _, err := checkBundleCurrent(context.Background(), f.k8s, &errw, path, f.request(), false); err != nil || errw.Len() != 0 {
		t.Fatalf("a bundle with the rotation's new CAs was refused or warned about: %v, %q", err, errw.String())
	}
}

func TestCredentialsNeverOverwriteTheCurrentFiles(t *testing.T) {
	f := newCredFixture(t, false, "")

	req := f.request()
	req.KubeconfigOut = f.kubePath

	if _, err := writeCredentials(context.Background(), f.k8s, req); err == nil {
		t.Fatal("the current kubeconfig was offered as the output")
	}
}

func TestCredentialsCommandDoesNotPrintCredentials(t *testing.T) {
	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)

	files, err := writeCredentials(context.Background(), f.k8s, f.request())
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	reportCredentials(&out, files)

	files.Dual, files.ForcedStaleBundle = false, true
	out.Reset()
	reportCredentials(&out, files)

	if !strings.Contains(out.String(), "the bundle's CAs, which differ") {
		t.Fatalf("a forced stale bundle is reported as the cluster's CA: %s", out.String())
	}

	files.ForcedStaleBundle = false
	out.Reset()
	reportCredentials(&out, files)

	for _, s := range slices.Concat(bundleSecretMaterial(f.old), []string{string(f.rot.Talos.Key), string(f.rot.Kubernetes.Key), "BEGIN"}) {
		if strings.Contains(out.String(), s) {
			t.Fatal("the report printed credential material")
		}
	}
}

func okChecks() handoffChecks {
	return handoffChecks{Kube: func(context.Context, string) error { return nil }}
}

func TestRotateCAHandoffWritesTheFilesChecksThemAndAcknowledges(t *testing.T) {
	shrinkFollowTimers(t)

	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)

	var checked string

	checks := handoffChecks{Kube: func(_ context.Context, path string) error {
		checked = path

		return nil
	}}

	var errw bytes.Buffer

	files, err := runRotateCAHandoff(context.Background(), handoffDeps{K8s: f.k8s, Checks: checks, Poll: time.Millisecond},
		f.request(), credOpName, &errw)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errw.String(), "checked: the new kubeconfig") {
		t.Fatalf("no line saying the kubeconfig was verified: %s", errw.String())
	}

	if checked != files.Kubeconfig {
		t.Fatalf("the new kubeconfig was not checked before the acknowledgement: %q", checked)
	}

	var op ichorv1alpha1.TalosOperation
	if err := f.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); err != nil {
		t.Fatal(err)
	}

	if got := op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation]; got != credHandoffToken {
		t.Fatalf("hand-off annotation %q", got)
	}

	if strings.Contains(errw.String(), "BEGIN") {
		t.Fatal("credential material was printed")
	}
}

func TestRotateCAHandoffDoesNotAcknowledgeCredentialsThatDoNotWork(t *testing.T) {
	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)

	checks := handoffChecks{Kube: func(context.Context, string) error {
		return errors.New("x509: certificate signed by unknown authority")
	}}

	_, err := runRotateCAHandoff(context.Background(), handoffDeps{K8s: f.k8s, Checks: checks, Poll: time.Millisecond},
		f.request(), credOpName, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "NOT acknowledging") {
		t.Fatalf("want a refusal to acknowledge, got %v", err)
	}

	var op ichorv1alpha1.TalosOperation
	if err := f.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); err != nil {
		t.Fatal(err)
	}

	if _, set := op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation]; set {
		t.Fatal("the rotation was released although the new credentials do not work")
	}
}

func TestRotateCAHandoffWaitsForTheGateAndStopsPastIt(t *testing.T) {
	shrinkFollowTimers(t)

	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAccepting)

	// The operation reaches the gate while the handoff polls.
	go func() {
		time.Sleep(30 * time.Millisecond)

		var op ichorv1alpha1.TalosOperation
		if err := f.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); err == nil {
			op.Status.CARotation.Stage = ichorv1alpha1.CARotationStageAwaitingCredentials
			_ = f.k8s.Update(context.Background(), &op)
		}
	}()

	if _, err := runRotateCAHandoff(context.Background(), handoffDeps{K8s: f.k8s, Checks: okChecks(), Poll: time.Millisecond},
		f.request(), credOpName, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	// A hand-off for an operation already past the gate is an error, not a silent no-op.
	past := newCredFixture(t, true, ichorv1alpha1.CARotationStageIssuing)
	if _, err := runRotateCAHandoff(context.Background(), handoffDeps{K8s: past.k8s, Checks: okChecks(), Poll: time.Millisecond},
		past.request(), credOpName, &bytes.Buffer{}); err == nil {
		t.Fatal("a rotation past the gate was handed off")
	}
}

func TestAcknowledgeNeedsAWaitingRotation(t *testing.T) {
	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAccepting)

	if _, err := acknowledgeHandoff(context.Background(), f.k8s, testNamespace, f.name, "", io.Discard, nil); err == nil {
		t.Fatal("acknowledged a rotation that is not at the gate")
	}
}

// flakyAckClient is the fixture's cluster behind a client whose first patch fails with firstErr, as when
// the API server restarts under it. patches counts the patch calls.
func flakyAckClient(t *testing.T, f *credFixture, firstErr error) (client.Client, *int) {
	t.Helper()

	var op ichorv1alpha1.TalosOperation
	if err := f.k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); err != nil {
		t.Fatal(err)
	}

	op.ResourceVersion = ""

	patches := new(int)

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(&op).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			*patches++
			if *patches == 1 {
				return firstErr
			}

			return c.Patch(ctx, obj, patch, opts...)
		},
	}).Build()

	return c, patches
}

func TestAcknowledgeRetriesAPatchThatDidNotLandWithTheNewClient(t *testing.T) {
	shrinkFollowTimers(t)

	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)
	c, patches := flakyAckClient(t, f, io.ErrUnexpectedEOF)

	redials := 0
	redial := func() (client.Client, error) {
		redials++

		return c, nil
	}

	var errw bytes.Buffer

	if _, err := acknowledgeHandoff(context.Background(), c, testNamespace, f.name, "", &errw, redial); err != nil {
		t.Fatalf("a patch that did not land was not sent again: %v\n%s", err, errw.String())
	}

	if *patches != 2 || redials != 1 {
		t.Fatalf("patches = %d, redials = %d; want the patch sent twice and the new client used once", *patches, redials)
	}

	if !strings.Contains(errw.String(), "retrying") {
		t.Fatalf("the retry was not reported: %s", errw.String())
	}

	var op ichorv1alpha1.TalosOperation
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: credOpName}, &op); err != nil {
		t.Fatal(err)
	}

	if got := op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation]; got != credHandoffToken {
		t.Fatalf("hand-off annotation %q", got)
	}
}

// The REST mapper failing to discover the ichor group while the API server comes back is retried with
// the same client, which is not cached into failing.
func TestAcknowledgeRetriesADiscoveryFailureWithTheSameClient(t *testing.T) {
	shrinkFollowTimers(t)

	f := newCredFixture(t, true, ichorv1alpha1.CARotationStageAwaitingCredentials)
	gv := ichorv1alpha1.GroupVersion
	discoveryFailed := apiutil.ErrResourceDiscoveryFailed{gv: apierrors.NewNotFound(schema.GroupResource{}, "")}
	c, patches := flakyAckClient(t, f, &discoveryFailed)

	var errw bytes.Buffer

	if _, err := acknowledgeHandoff(context.Background(), c, testNamespace, f.name, "", &errw, nil); err != nil {
		t.Fatalf("a discovery failure ended the hand-off: %v\n%s", err, errw.String())
	}

	if *patches != 2 {
		t.Fatalf("patches = %d, want 2", *patches)
	}
}

func TestWatchCARotationSurvivesTheAPIRestartingAndFollowsToTheEnd(t *testing.T) {
	shrinkFollowTimers(t)

	gets := 0

	flaky := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			gets++

			switch {
			case gets <= 2:
				return &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}
			}

			if op, ok := obj.(*ichorv1alpha1.TalosOperation); ok {
				op.Name, op.Namespace = key.Name, key.Namespace
				op.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
				op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{Stage: ichorv1alpha1.CARotationStageDone}
			}

			return nil
		},
	}).Build()

	var errw bytes.Buffer

	if err := watchCARotation(context.Background(), flaky, testNamespace, credOpName, &errw, nil); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	if !strings.Contains(errw.String(), "API unreachable, retrying") {
		t.Fatalf("the retries were not reported: %s", errw.String())
	}
}

func TestPassedStagesAreTheOnesBetweenTwoPolls(t *testing.T) {
	a, aw, i, d, f, done := ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials,
		ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing,
		ichorv1alpha1.CARotationStageDone

	for _, tc := range []struct {
		last, stage string
		want        []string
	}{
		{"", a, nil},
		{"", i, []string{a, aw}},
		{a, i, []string{aw}},
		{i, i, nil},
		{f, done, nil},
		{a, done, []string{aw, i, d, f}},
		{done, a, nil},
		{a, "", nil},
	} {
		if got := passedStages(tc.last, tc.stage); !slices.Equal(got, tc.want) {
			t.Errorf("passedStages(%q, %q) = %v, want %v", tc.last, tc.stage, got, tc.want)
		}
	}
}

// TestWatchCARotationPrintsTheStagesAPollJumpedOver: the passes are shorter than the poll interval, so
// the poll sees Accepting, then Issuing; the stages in between are still printed, in order.
func TestWatchCARotationPrintsTheStagesAPollJumpedOver(t *testing.T) {
	shrinkFollowTimers(t)

	seen := []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDone,
	}
	polls := 0

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			op, ok := obj.(*ichorv1alpha1.TalosOperation)
			if !ok {
				return nil
			}

			op.Name, op.Namespace = key.Name, key.Namespace
			op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
			op.Status.CARotation = &ichorv1alpha1.TalosOperationCARotationStatus{Stage: seen[min(polls, len(seen)-1)]}

			if op.Status.CARotation.Stage == ichorv1alpha1.CARotationStageDone {
				op.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
			}

			polls++

			return nil
		},
	}).Build()

	var errw bytes.Buffer
	if err := watchCARotation(context.Background(), k8s, testNamespace, credOpName, &errw, nil); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	want := []string{
		"stage " + ichorv1alpha1.CARotationStageAccepting,
		"stage " + ichorv1alpha1.CARotationStageAwaitingCredentials,
		"stage " + ichorv1alpha1.CARotationStageIssuing,
		"stage " + ichorv1alpha1.CARotationStageDropping,
		"stage " + ichorv1alpha1.CARotationStageFinalizing,
		"stage " + ichorv1alpha1.CARotationStageDone,
	}

	at := 0

	for _, w := range want {
		i := strings.Index(errw.String()[at:], w)
		if i < 0 {
			t.Fatalf("no line for %q after position %d:\n%s", w, at, errw.String())
		}

		at += i + len(w)
	}
}

func TestWatchRebuildsATunnelThatDiesAndSaysWhyItCouldNotAtFirst(t *testing.T) {
	shrinkFollowTimers(t)

	dead := &flakyOps{errs: []error{connect503()}}
	fresh := &flakyOps{}
	attempts := 0

	c := &apiClients{Operation: dead, Close: func() {}, Reconnect: func(context.Context) (*apiClients, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("x509: certificate signed by unknown authority")
		}

		return &apiClients{Operation: fresh, Close: func() {}}, nil
	}}

	// The first tunnel dies, and so does the call the first reconnect leaves in place.
	dead.errs = append(dead.errs, connect503())

	var errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &errw); err != nil {
		t.Fatal(err)
	}

	if attempts != 2 || fresh.calls != 1 {
		t.Fatalf("reconnect attempts %d, fresh calls %d", attempts, fresh.calls)
	}

	if !strings.Contains(errw.String(), "could not re-open the tunnel to ichor-ui (x509: certificate signed by unknown authority)") {
		t.Fatalf("the reason the tunnel could not be rebuilt was swallowed:\n%s", errw.String())
	}
}

func connect503() error { return connect.NewError(connect.CodeUnavailable, errors.New("tunnel gone")) }
