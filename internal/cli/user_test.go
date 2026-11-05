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
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const (
	testUserPassword = "correct horse battery staple"
	testGenerated    = "Gen3ratedPassw0rdValue24"
	testToken        = "tok-secret-ichor-ui-id-token"
	testUserAlice    = "alice"
	testAdminName    = "root"
)

// fakeUserService records what the CLI sends and answers like ichor-ui would.
type fakeUserService struct {
	ichorv1alpha1connect.UnimplementedUserServiceHandler

	auth     string
	created  *pb.CreateUserRequest
	updated  *pb.UpdateUserRequest
	setPw    *pb.SetPasswordRequest
	kubecfg  *pb.GetMyKubeconfigRequest
	existing *pb.User
}

func (f *fakeUserService) CreateUser(_ context.Context, r *connect.Request[pb.CreateUserRequest]) (*connect.Response[pb.CreateUserResponse], error) {
	f.created = r.Msg

	return connect.NewResponse(&pb.CreateUserResponse{User: &pb.User{Name: r.Msg.GetName(), Roles: r.Msg.GetRoles()}}), nil
}

func (f *fakeUserService) GetUser(context.Context, *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.GetUserResponse], error) {
	return connect.NewResponse(&pb.GetUserResponse{User: f.existing}), nil
}

func (f *fakeUserService) UpdateUser(_ context.Context, r *connect.Request[pb.UpdateUserRequest]) (*connect.Response[pb.UpdateUserResponse], error) {
	f.updated = r.Msg

	return connect.NewResponse(&pb.UpdateUserResponse{User: &pb.User{Name: r.Msg.GetName()}}), nil
}

func (f *fakeUserService) SetPassword(_ context.Context, r *connect.Request[pb.SetPasswordRequest]) (*connect.Response[pb.SetPasswordResponse], error) {
	f.setPw = r.Msg
	if r.Msg.GetPassword() == "" {
		return connect.NewResponse(&pb.SetPasswordResponse{GeneratedPassword: testGenerated}), nil
	}

	return connect.NewResponse(&pb.SetPasswordResponse{}), nil
}

func (f *fakeUserService) GetMyKubeconfig(_ context.Context, r *connect.Request[pb.GetMyKubeconfigRequest]) (*connect.Response[pb.GetMyKubeconfigResponse], error) {
	f.kubecfg = r.Msg

	return connect.NewResponse(&pb.GetMyKubeconfigResponse{Kubeconfig: "apiVersion: v1\nkind: Config\n", Filename: "ichor-c1.kubeconfig"}), nil
}

func (f *fakeUserService) WhoAmI(context.Context, *connect.Request[pb.WhoAmIRequest]) (*connect.Response[pb.WhoAmIResponse], error) {
	return connect.NewResponse(&pb.WhoAmIResponse{Username: "ichor:alice", Groups: []string{"ichor:admin"}, Source: appNameIchor, MustChangePassword: true}), nil
}

func newUserClient(t *testing.T, f *fakeUserService) ichorv1alpha1connect.UserServiceClient {
	t.Helper()

	mux := http.NewServeMux()
	path, h := ichorv1alpha1connect.NewUserServiceHandler(f)
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.auth = r.Header.Get("Authorization")
		h.ServeHTTP(w, r)
	}))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	hc := &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: testToken}}

	return ichorv1alpha1connect.NewUserServiceClient(hc, srv.URL)
}

func newUserCmd(stdin string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errw)

	return cmd, &out, &errw
}

func resetUserOpts(t *testing.T) {
	t.Helper()

	old, oldFmt := userOpts, outputFormat
	userOpts, outputFormat = userFlags{}, outputJSON

	t.Cleanup(func() { userOpts, outputFormat = old, oldFmt })
}

func TestUserCreateReadsPasswordFromStdinAndSendsBearer(t *testing.T) {
	resetUserOpts(t)

	f := &fakeUserService{}
	u := newUserClient(t, f)
	cmd, out, errw := newUserCmd(testUserPassword + "\n")
	userOpts.passwordStdin, userOpts.roles = true, []string{ichorauth.RoleViewer}

	if err := runUserCreate(context.Background(), u, cmd, testUserAlice); err != nil {
		t.Fatal(err)
	}

	if f.created.GetName() != testUserAlice || f.created.GetRoles()[0] != ichorauth.RoleViewer {
		t.Errorf("create request = %v", f.created)
	}

	if f.setPw.GetPassword() != testUserPassword {
		t.Error("the password from stdin did not reach SetPassword")
	}

	if f.auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q", f.auth)
	}

	if strings.Contains(out.String()+errw.String(), testUserPassword) {
		t.Error("the password was echoed")
	}
}

func TestUserCreateGeneratePrintsOnceToStderr(t *testing.T) {
	resetUserOpts(t)

	f := &fakeUserService{}
	cmd, out, errw := newUserCmd("")
	userOpts.generate = true

	if err := runUserCreate(context.Background(), newUserClient(t, f), cmd, "bob"); err != nil {
		t.Fatal(err)
	}

	if f.setPw.GetPassword() != "" {
		t.Error("--generate must ask the server to generate")
	}

	if strings.Count(errw.String(), testGenerated) != 1 {
		t.Errorf("generated password should be printed exactly once on stderr: %q", errw.String())
	}

	if strings.Contains(out.String(), testGenerated) {
		t.Error("generated password leaked to stdout")
	}
}

func TestResolvePassword(t *testing.T) {
	cmd, _, _ := newUserCmd("")

	if _, err := resolvePassword(cmd, true, true); err == nil {
		t.Error("--password-stdin with --generate should be refused")
	}

	cmd, _, _ = newUserCmd("\n")
	if _, err := resolvePassword(cmd, true, false); err == nil {
		t.Error("an empty stdin line is not a password")
	}

	old := readPasswordFn
	t.Cleanup(func() { readPasswordFn = old })

	answers := []string{"one-long-password-x", "another-long-password"}
	readPasswordFn = func(string, io.Writer) (string, error) {
		a := answers[0]
		answers = answers[1:]

		return a, nil
	}

	cmd, _, _ = newUserCmd("")
	if _, err := resolvePassword(cmd, false, false); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Errorf("mismatching prompts: %v", err)
	}

	readPasswordFn = func(string, io.Writer) (string, error) { return "", errors.New("no terminal") }
	if _, err := resolvePassword(cmd, false, false); err == nil {
		t.Error("no terminal should fail")
	}
}

func TestUserUpdateChangesOnlyPassedFlags(t *testing.T) {
	resetUserOpts(t)

	f := &fakeUserService{existing: &pb.User{Name: testUserAlice, DisplayName: "Alice", Email: "a@example.com", Roles: []string{ichorauth.RoleViewer}}}
	cmd, _, _ := newUserCmd("")
	cmd.Flags().BoolVar(&userOpts.disabled, "disabled", false, "")
	cmd.Flags().StringSliceVar(&userOpts.roles, "role", nil, "")
	cmd.Flags().StringVar(&userOpts.email, "email", "", "")
	cmd.Flags().StringVar(&userOpts.displayName, "display-name", "", "")

	if err := runUserUpdate(context.Background(), newUserClient(t, f), cmd, testUserAlice); err == nil {
		t.Fatal("no flag passed should be an error")
	}

	if err := cmd.Flags().Set("disabled", "true"); err != nil {
		t.Fatal(err)
	}

	if err := runUserUpdate(context.Background(), newUserClient(t, f), cmd, testUserAlice); err != nil {
		t.Fatal(err)
	}

	u := f.updated
	if !u.GetDisabled() || u.GetDisplayName() != "Alice" || u.GetEmail() != "a@example.com" || len(u.GetRoles()) != 1 {
		t.Errorf("update request = %v; only disabled should have changed", u)
	}
}

func TestAuthStatusPrintsNoToken(t *testing.T) {
	f := &fakeUserService{}
	sess := &uiSession{Users: newUserClient(t, f), Issuer: testUIURL,
		Entry: &authCacheEntry{IDToken: testToken, RefreshToken: "refresh-secret", Expiry: time.Now().Add(time.Minute)}}

	t.Setenv("HOME", t.TempDir())

	var out bytes.Buffer
	if err := runAuthStatus(context.Background(), sess, &out); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"ichor:alice", "ichor:admin", "must be changed", "expires"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}

	for _, banned := range []string{testToken, "refresh-secret"} {
		if strings.Contains(out.String(), banned) {
			t.Errorf("status printed a token: %q", banned)
		}
	}
}

func TestAuthKubeconfigWrites0600AndPassesCAFile(t *testing.T) {
	f := &fakeUserService{}
	sess := &uiSession{Users: newUserClient(t, f), Issuer: testUIURL, Entry: &authCacheEntry{}}
	dir := t.TempDir()
	out := filepath.Join(dir, "kc.yaml")

	var stdout, stderr bytes.Buffer
	if err := runAuthKubeconfig(context.Background(), sess, out, "ca.pem", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("kubeconfig file: %v %v", st, err)
	}

	if !filepath.IsAbs(f.kubecfg.GetCaFile()) || !strings.HasSuffix(f.kubecfg.GetCaFile(), "ca.pem") {
		t.Errorf("ca_file sent = %q, want an absolute path", f.kubecfg.GetCaFile())
	}

	if err := runAuthKubeconfig(context.Background(), sess, out, "", &stdout, &stderr); err == nil {
		t.Error("overwriting an existing file should be refused")
	}

	if err := runAuthKubeconfig(context.Background(), sess, "-", "", &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "kind: Config") {
		t.Errorf("stdout mode: %v %q", err, stdout.String())
	}

	if f.kubecfg.GetCaFile() != "" {
		t.Error("no --ca-file must send no ca_file")
	}
}

func TestAuthHTTPClientTrustsOnlyTheGivenCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	hc, err := authHTTPClient(caPath)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := hc.Get(srv.URL) //nolint:noctx // test
	if err != nil {
		t.Fatalf("with the CA: %v", err)
	}

	_ = resp.Body.Close()

	if _, err := http.Get(srv.URL); err == nil { //nolint:noctx,bodyclose // test
		t.Error("without --ca-file the private CA must not be trusted")
	}

	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := authHTTPClient(bad); err == nil {
		t.Error("a file without a certificate should be refused")
	}

	if hc, err := authHTTPClient(""); hc != nil || err != nil {
		t.Errorf("empty --ca-file: %v %v", hc, err)
	}
}

func TestSecureIssuerRefusesPlainHTTP(t *testing.T) {
	for raw, ok := range map[string]bool{
		testUIURL: true, "http://127.0.0.1:8080": true, "http://localhost:9": true,
		"http://ichor.example.com": false, "ichor.example.com": false,
	} {
		if err := secureIssuer(raw); (err == nil) != ok {
			t.Errorf("secureIssuer(%q) = %v, want ok=%v", raw, err, ok)
		}
	}
}

func userKubeClient(t *testing.T, withSecret bool) client.Client {
	t.Helper()

	b := fake.NewClientBuilder().WithScheme(newTestScheme(t))
	if withSecret {
		b = b.WithObjects(&corev1.Secret{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName})
	}

	return b.Build()
}

func TestBootstrapUserWritesCRAndHash(t *testing.T) {
	k := userKubeClient(t, true)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	spec := userSpec{Name: testAdminName, Roles: []string{ichorauth.RoleAdmin}}

	if err := createUserWithPassword(context.Background(), k, spec, testUserPassword, now, io.Discard); err != nil {
		t.Fatal(err)
	}

	var u ichorv1alpha1.IchorUser
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: testAdminName}, &u); err != nil || u.Spec.Roles[0] != ichorauth.RoleAdmin {
		t.Fatalf("user CR: %v %+v", err, u)
	}

	var sec corev1.Secret
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName}, &sec); err != nil {
		t.Fatal(err)
	}

	cred, err := ichorauth.ParseCredential(sec.Data[testAdminName])
	if err != nil || !cred.MustChange || !cred.NotBefore.Equal(now) {
		t.Fatalf("credential: %v %+v", err, cred)
	}

	if ok, _, err := cred.Verify(testUserPassword); err != nil || !ok {
		t.Error("the stored hash does not verify the password")
	}

	if bytes.Contains(sec.Data[testAdminName], []byte(testUserPassword)) {
		t.Error("the Secret holds the plain password")
	}
}

func TestBootstrapUserRefusals(t *testing.T) {
	ctx := context.Background()
	spec := userSpec{Name: testAdminName, Roles: []string{ichorauth.RoleAdmin}}

	// An existing user: never reset its password.
	k := userKubeClient(t, true)
	if err := createUserWithPassword(ctx, k, spec, testUserPassword, time.Now(), io.Discard); err != nil {
		t.Fatal(err)
	}

	err := createUserWithPassword(ctx, k, spec, "another password entirely", time.Now(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing user: %v", err)
	}

	// Users not turned on: no credentials Secret.
	err = createUserWithPassword(ctx, userKubeClient(t, false), spec, testUserPassword, time.Now(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not turned on") {
		t.Errorf("no Secret: %v", err)
	}

	// Policy and name checks leave nothing behind.
	k = userKubeClient(t, true)
	for _, bad := range []userSpec{{Name: "Root"}, {Name: "ok", Roles: []string{"Bad Role"}}} {
		if err := createUserWithPassword(ctx, k, bad, testUserPassword, time.Now(), io.Discard); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}

	if err := createUserWithPassword(ctx, k, userSpec{Name: "ok"}, "short", time.Now(), io.Discard); err == nil {
		t.Errorf("short password: %v", err)
	}

	var list ichorv1alpha1.IchorUserList
	if err := k.List(ctx, &list); err != nil || len(list.Items) != 0 {
		t.Errorf("a refused user left a CR behind: %v %d", err, len(list.Items))
	}
}
