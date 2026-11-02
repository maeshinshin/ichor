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

package uiapi

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const (
	usersNS         = "ichor-system"
	userAlice       = "alice"
	userRoot        = "root"
	ichorAdminGroup = "ichor:admin"
	strongPass      = "correct horse battery"
	strongPass2     = "another long password"
)

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

type fakeLocal map[string]string // token -> subject

func (f fakeLocal) Verify(_ context.Context, raw string) (string, time.Time, error) {
	if sub, ok := f[raw[strings.LastIndex(raw, ".")+1:]]; ok {
		return sub, time.Now(), nil
	}

	return "", time.Time{}, errors.New("bad")
}

// fakeLocalAt is fakeLocal with a chosen issue time per token.
type fakeLocalAt map[string]time.Time

func (f fakeLocalAt) Verify(_ context.Context, raw string) (string, time.Time, error) {
	if iat, ok := f[raw[strings.LastIndex(raw, ".")+1:]]; ok {
		return userAlice, iat, nil
	}

	return "", time.Time{}, errors.New("bad")
}

func newUser(name string, disabled bool, roles ...string) *ichorv1alpha1.IchorUser {
	return &ichorv1alpha1.IchorUser{
		Namespace: usersNS, Name: name,
		Spec: ichorv1alpha1.IchorUserSpec{DisplayName: "D " + name, Roles: roles, Disabled: disabled},
	}
}

// userServer builds a Server on a fake client. allow answers every
// SelfSubjectAccessReview.
func userServer(t *testing.T, allow bool, objs ...client.Object) *Server {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	store := &corev1.Secret{Namespace: usersNS, Name: ichorauth.CredentialsSecretName}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objs, store)...).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if r, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				r.Status.Allowed = allow

				return nil
			}

			return c.Create(ctx, obj, opts...)
		}}).Build()

	return &Server{
		Client: c, ControllerNamespace: usersNS,
		Users: NewUserSettings(&UserCache{Reader: c, Namespace: usersNS}, "https://ichor.example.com"),
	}
}

func ichorCtx(name string) context.Context {
	return context.WithValue(context.Background(), identityKey{}, Identity{User: IchorUserPrefix + name, Source: SourceIchor, Groups: []string{ichorAdminGroup}})
}

func TestAuthenticateLocalTokenReadsRolesFromTheCRAndRefusesDisabled(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, adminRole, "ops"), newUser("bob", true, "viewer"))
	a := (*Authenticator)(nil).WithLocal("https://ichor.example.com", fakeLocal{"ta": userAlice, "tb": "bob", "tg": "ghost"}, s.Users.Cache)

	mk := func(tok string) string { // a JWT-shaped token whose unverified iss is the local issuer
		payload := `{"iss":"https://ichor.example.com"}`
		return "e30." + b64(payload) + "." + tok
	}

	id, err := a.Authenticate(context.Background(), "Bearer "+mk("ta"))
	if err != nil {
		t.Fatal(err)
	}

	if id.User != "ichor:alice" || !slices.Equal(id.Groups, []string{ichorAdminGroup, "ichor:ops"}) || id.Source != SourceIchor {
		t.Errorf("identity = %+v", id)
	}

	for _, tok := range []string{"tb", "tg", "nope"} {
		if _, err := a.Authenticate(context.Background(), "Bearer "+mk(tok)); err == nil {
			t.Errorf("token %q accepted", tok)
		}
	}
	// A token naming another issuer never reaches the local verifier.
	if _, err := a.Authenticate(context.Background(), "Bearer e30."+b64(`{"iss":"https://evil"}`)+".ta"); err == nil {
		t.Error("a token of another issuer was accepted")
	}
}

func TestAuthenticateLocalSeesADisableWithinTheCacheWindow(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, adminRole))
	now := time.Now()
	s.Users.Cache.Now = func() time.Time { return now }

	a := (*Authenticator)(nil).WithLocal("https://i", fakeLocal{"t": userAlice}, s.Users.Cache)
	tok := "Bearer e30." + b64(`{"iss":"https://i"}`) + ".t"

	if _, err := a.Authenticate(context.Background(), tok); err != nil {
		t.Fatal(err)
	}

	u := newUser(userAlice, true, adminRole)
	cur := &ichorv1alpha1.IchorUser{}

	if err := s.Client.Get(context.Background(), client.ObjectKeyFromObject(u), cur); err != nil {
		t.Fatal(err)
	}

	cur.Spec.Disabled = true
	if err := s.Client.Update(context.Background(), cur); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Authenticate(context.Background(), tok); err != nil {
		t.Fatal("cache should still hold the old user inside the window")
	}

	now = now.Add(UserCacheTTL + time.Second)

	if _, err := a.Authenticate(context.Background(), tok); err == nil {
		t.Fatal("a disabled user was accepted after the cache window")
	}
}

func TestExternalTokenCannotNameAnIchorIdentity(t *testing.T) {
	p := newFakeIdP(t)

	a, err := NewAuthenticator(context.Background(), OIDCConfig{Issuer: p.srv.URL, ClientID: testClientID})
	if err != nil {
		t.Fatal(err)
	}

	tok := p.mint(t, p.key, func(c map[string]any) { c["email"] = ichorAdminGroup })
	if _, err := a.Authenticate(context.Background(), "Bearer "+tok); err == nil {
		t.Fatal("an external token named ichor:admin")
	}

	tok = p.mint(t, p.key, func(c map[string]any) { c["groups"] = []string{ichorAdminGroup} })

	id, err := a.Authenticate(context.Background(), "Bearer "+tok)
	if err != nil || slices.Contains(id.Groups, ichorAdminGroup) {
		t.Fatalf("an external token got the group ichor:admin: %v %v", id.Groups, err)
	}
}

func TestUserServiceNeedsUsersOnAndAnIdentity(t *testing.T) {
	s := userServer(t, true)
	s.Users = nil

	_, err := s.ListUsers(ichorCtx("a"), connect.NewRequest(&pb.ListUsersRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("users off: %v", err)
	}

	s = userServer(t, true)

	_, err = s.ListUsers(context.Background(), connect.NewRequest(&pb.ListUsersRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("no identity: %v", err)
	}
}

func TestCreateUpdateDisableAndTheLastAdminGuard(t *testing.T) {
	s := userServer(t, true)
	ctx := ichorCtx(userRoot)

	if _, err := s.CreateUser(ctx, connect.NewRequest(&pb.CreateUserRequest{Name: "Bad_Name"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("bad name: %v", err)
	}

	if _, err := s.CreateUser(ctx, connect.NewRequest(&pb.CreateUserRequest{Name: userRoot, Roles: []string{adminRole}})); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateUser(ctx, connect.NewRequest(&pb.CreateUserRequest{Name: userRoot})); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("duplicate: %v", err)
	}

	for name, req := range map[string]*pb.UpdateUserRequest{
		"disable": {Name: userRoot, Roles: []string{adminRole}, Disabled: true},
		"demote":  {Name: userRoot, Roles: []string{"viewer"}},
	} {
		if _, err := s.UpdateUser(ctx, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("%s the last admin: %v", name, err)
		}
	}

	if _, err := s.DeleteUser(ctx, connect.NewRequest(&pb.DeleteUserRequest{Name: userRoot})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("delete the last admin: %v", err)
	}

	// With a second admin, the first can go.
	if _, err := s.CreateUser(ctx, connect.NewRequest(&pb.CreateUserRequest{Name: "second", Roles: []string{adminRole}})); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateUser(ctx, connect.NewRequest(&pb.UpdateUserRequest{Name: userRoot, Roles: []string{adminRole}, Disabled: true})); err != nil {
		t.Errorf("disable with a second admin: %v", err)
	}

	list, err := s.ListUsers(ctx, connect.NewRequest(&pb.ListUsersRequest{}))
	if err != nil || len(list.Msg.GetUsers()) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
}

func readStored(t *testing.T, s *Server) (ichorauth.Credential, string) {
	t.Helper()

	var sec corev1.Secret
	if err := s.Client.Get(context.Background(), client.ObjectKey{Namespace: usersNS, Name: ichorauth.CredentialsSecretName}, &sec); err != nil {
		t.Fatal(err)
	}

	c, err := ichorauth.ParseCredential(sec.Data[userAlice])
	if err != nil {
		t.Fatal(err)
	}

	return c, string(sec.Data[userAlice])
}

func TestSetPasswordNeedsTheVerbAndNeverStoresCleartext(t *testing.T) {
	denied := userServer(t, false, newUser(userAlice, false, "viewer"))
	if _, err := denied.SetPassword(ichorCtx("x"), connect.NewRequest(&pb.SetPasswordRequest{Name: userAlice, Password: strongPass})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("without the verb: %v", err)
	}

	if _, found, _ := denied.readCredential(context.Background(), userAlice); found {
		t.Fatal("a refused SetPassword wrote an entry")
	}

	s := userServer(t, true, newUser(userAlice, false, "viewer"))

	resp, err := s.SetPassword(ichorCtx("x"), connect.NewRequest(&pb.SetPasswordRequest{Name: userAlice, Password: strongPass}))
	if err != nil || resp.Msg.GetGeneratedPassword() != "" {
		t.Fatalf("explicit password: %v %q", err, resp.Msg.GetGeneratedPassword())
	}

	cred, raw := readStored(t, s)
	if !cred.MustChange || !strings.HasPrefix(cred.Hash, "$argon2id$") || strings.Contains(raw, strongPass) {
		t.Errorf("stored entry wrong: mustChange=%v", cred.MustChange)
	}

	if _, err := s.SetPassword(ichorCtx("x"), connect.NewRequest(&pb.SetPasswordRequest{Name: userAlice, Password: "short"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("short password: %v", err)
	}

	gen, err := s.SetPassword(ichorCtx("x"), connect.NewRequest(&pb.SetPasswordRequest{Name: userAlice}))
	if err != nil || len(gen.Msg.GetGeneratedPassword()) != 24 {
		t.Fatalf("generated: %v", err)
	}

	cred, raw = readStored(t, s)
	if strings.Contains(raw, gen.Msg.GetGeneratedPassword()) || !pwMatches(gen.Msg.GetGeneratedPassword(), cred.Hash) {
		t.Error("generated password not stored as a matching hash")
	}

	if _, err := s.SetPassword(ichorCtx("x"), connect.NewRequest(&pb.SetPasswordRequest{Name: "nobody"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown user: %v", err)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	s := userServer(t, false, newUser(userAlice, false, "viewer"))
	if err := s.setCredential(context.Background(), userAlice, strongPass, true); err != nil {
		t.Fatal(err)
	}

	change := func(old, nu string) error {
		_, err := s.ChangeOwnPassword(ichorCtx(userAlice), connect.NewRequest(&pb.ChangeOwnPasswordRequest{OldPassword: old, NewPassword: nu}))
		return err
	}

	err := change("wrong password!!", strongPass2)
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "invalid credentials") {
		t.Fatalf("wrong old password: %v", err)
	}

	if err := change(strongPass, "short"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("short new password: %v", err)
	}

	if err := change(strongPass, strongPass2); err != nil {
		t.Fatal(err)
	}

	cred, _ := readStored(t, s)
	if cred.MustChange || !pwMatches(strongPass2, cred.Hash) {
		t.Error("password not changed or still must-change")
	}

	// An external user has no password here, and a user with no entry is refused the same way.
	ext := context.WithValue(context.Background(), identityKey{}, Identity{User: "oidc:bob", Source: SourceOIDC})
	if _, err := s.ChangeOwnPassword(ext, connect.NewRequest(&pb.ChangeOwnPasswordRequest{OldPassword: "x", NewPassword: strongPass})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("external user: %v", err)
	}

	if _, err := s.ChangeOwnPassword(ichorCtx("ghost"), connect.NewRequest(&pb.ChangeOwnPasswordRequest{OldPassword: strongPass, NewPassword: strongPass2})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("no entry: %v", err)
	}
}

func TestChangeOwnPasswordBacksOffAfterRepeatedFailures(t *testing.T) {
	s := userServer(t, false, newUser(userAlice, false, "viewer"))
	if err := s.setCredential(context.Background(), userAlice, strongPass, false); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }

	t.Cleanup(func() { changePasswordFailures.Reset(userAlice) })
	changePasswordFailures.Reset(userAlice)

	change := func(old string) error {
		_, err := s.ChangeOwnPassword(ichorCtx(userAlice), connect.NewRequest(&pb.ChangeOwnPasswordRequest{OldPassword: old, NewPassword: strongPass2}))

		return err
	}

	for range 5 { // the back-off's free failures (oidcissuer.Backoff)
		if err := change("wrong password!!"); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("wrong old password: %v", err)
		}
	}

	// Even the right password waits while the back-off runs.
	if err := change(strongPass); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("during back-off: %v", err)
	}

	now = now.Add(2 * time.Second)
	if err := change(strongPass); err != nil {
		t.Fatalf("after back-off: %v", err)
	}

	if changePasswordFailures.Wait(userAlice, now) != 0 {
		t.Error("a success did not reset the back-off")
	}
}

func TestRevokeSessionsMovesNotBefore(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, "viewer"))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return t0 }

	if _, err := s.RevokeSessions(ichorCtx("x"), connect.NewRequest(&pb.RevokeSessionsRequest{Name: userAlice})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("no password yet: %v", err)
	}

	if err := s.setCredential(context.Background(), userAlice, strongPass, false); err != nil {
		t.Fatal(err)
	}

	s.Now = func() time.Time { return t0.Add(time.Hour) }

	if _, err := s.RevokeSessions(ichorCtx("x"), connect.NewRequest(&pb.RevokeSessionsRequest{Name: userAlice})); err != nil {
		t.Fatal(err)
	}

	cred, _ := readStored(t, s)
	if !cred.NotBefore.Equal(t0.Add(time.Hour)) || !pwMatches(strongPass, cred.Hash) {
		t.Errorf("notBefore = %v, or the hash changed", cred.NotBefore)
	}
}

func TestKubeconfigHoldsPublicDataOnly(t *testing.T) {
	raw, err := buildKubeconfig("c1", "https://10.0.0.5:6443", "", []byte("CA-PEM"), userAlice, "https://ichor.example.com", "")
	if err != nil {
		t.Fatal(err)
	}

	got := string(raw)
	for _, want := range []string{"command: ichor", "client.authentication.k8s.io/v1", "interactiveMode: IfAvailable",
		"- auth", "- token", "ichor-kube", "https://ichor.example.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("kubeconfig lacks %q:\n%s", want, got)
		}
	}

	for _, banned := range []string{"client-key", "token:", "password", "client-certificate"} {
		if strings.Contains(got, banned) {
			t.Errorf("kubeconfig contains %q", banned)
		}
	}
}

func TestWhoAmIReportsMustChange(t *testing.T) {
	s := userServer(t, false, newUser(userAlice, false, "viewer"))
	if err := s.setCredential(context.Background(), userAlice, strongPass, true); err != nil {
		t.Fatal(err)
	}

	resp, err := s.WhoAmI(ichorCtx(userAlice), connect.NewRequest(&pb.WhoAmIRequest{}))
	if err != nil || !resp.Msg.GetMustChangePassword() || resp.Msg.GetUsername() != "ichor:alice" {
		t.Fatalf("whoami: %v %v", resp, err)
	}
}

func TestKubeconfigCAFileOnlyWhenAsked(t *testing.T) {
	without, err := buildKubeconfig("c1", "https://10.0.0.5:6443", "", []byte("CA"), userAlice, "https://i.example.com", "")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(without), "ca-file") {
		t.Errorf("no ca_file requested, yet:\n%s", without)
	}

	with, err := buildKubeconfig("c1", "https://10.0.0.5:6443", "", []byte("CA"), userAlice, "https://i.example.com", "/etc/ichor/ca.pem")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(with), "- --ca-file\n") || !strings.Contains(string(with), "/etc/ichor/ca.pem") {
		t.Errorf("ca_file requested, but the exec args lack it:\n%s", with)
	}
}

func pwMatches(password, hash string) bool {
	ok, _, err := ichorauth.Verify(password, hash)

	return err == nil && ok
}

func TestKubeconfigServerOverride(t *testing.T) {
	u := NewUserSettings(nil, "https://i.example.com")
	if srv, name := u.kubeServer("10.0.0.5"); srv != "https://10.0.0.5:6443" || name != "" {
		t.Errorf("default = %q %q", srv, name)
	}

	u.KubeAPIServerURL, u.KubeAPITLSServerName = "https://k8s.example.com:443", "api.internal"

	srv, name := u.kubeServer("10.0.0.5")
	if srv != "https://k8s.example.com:443" || name != "api.internal" {
		t.Fatalf("override = %q %q", srv, name)
	}

	raw, err := buildKubeconfig("c1", srv, name, []byte("CA"), userAlice, "https://i.example.com", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"server: https://k8s.example.com:443", "tls-server-name: api.internal"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("kubeconfig lacks %q:\n%s", want, raw)
		}
	}
}

func TestMustChangeUserOnlyReachesWhoAmIAndChangeOwnPassword(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, adminRole))
	ctx := context.Background()
	now := time.Now()
	s.Users.Cache.Now = func() time.Time { return now }

	if err := s.setCredential(ctx, userAlice, strongPass, true); err != nil {
		t.Fatal(err)
	}

	a := (*Authenticator)(nil).WithLocal("https://i", fakeLocal{"t": userAlice}, s.Users.Cache)
	tok := "Bearer e30." + b64(`{"iss":"https://i"}`) + ".t"
	i := authInterceptor{a}

	for proc, ok := range map[string]bool{
		ichorv1alpha1connect.UserServiceWhoAmIProcedure:            true,
		ichorv1alpha1connect.UserServiceChangeOwnPasswordProcedure: true,
		ichorv1alpha1connect.UserServiceListUsersProcedure:         false,
		ichorv1alpha1connect.UserServiceGetMyKubeconfigProcedure:   false,
	} {
		_, err := i.auth(ctx, tok, proc)
		if ok && err != nil {
			t.Errorf("%s refused: %v", proc, err)
		}

		if !ok && connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: want PermissionDenied, got %v", proc, err)
		}
	}

	// Changing the password lifts the block at once on this replica.
	if err := s.setCredential(ctx, userAlice, strongPass2, false); err != nil {
		t.Fatal(err)
	}

	if _, err := i.auth(ctx, tok, ichorv1alpha1connect.UserServiceListUsersProcedure); err != nil {
		t.Errorf("still blocked after the change: %v", err)
	}
}
