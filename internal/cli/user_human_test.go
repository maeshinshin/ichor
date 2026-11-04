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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// Golden tests of the human output of `ichor user`, on a pipe: no colour codes.

const (
	userBob          = "bob"
	userAliceDisplay = "Alice"
	userAliceEmail   = "alice@example.com"
)

func usersFixture() []*pb.User {
	return []*pb.User{
		{Name: testUserAlice, DisplayName: userAliceDisplay, Email: userAliceEmail, Roles: []string{ichorauth.RoleAdmin}, PasswordSet: true, CreatedAt: ago(48 * time.Hour)},
		{Name: userBob, Roles: []string{ichorauth.RoleViewer, ichorauth.RoleOperator}, PasswordSet: true, Disabled: true, CreatedAt: ago(3 * time.Hour)},
		{Name: "carol", DisplayName: "Carol", Roles: []string{ichorauth.RoleViewer}, CreatedAt: ago(30 * time.Second)},
	}
}

func TestUserListHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderUserList(newHuman(&out), &pb.ListUsersResponse{Users: usersFixture()}); err != nil {
		t.Fatal(err)
	}

	want := "NAME   DISPLAY NAME  EMAIL              ROLES             STATE        AGE\n" +
		"alice  Alice         alice@example.com  admin             active       2d\n" +
		"bob    -             -                  viewer, operator  disabled     3h\n" +
		"carol  Carol         -                  viewer            no password  30s\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()

	if err := renderUserList(newHuman(&out), &pb.ListUsersResponse{}); err != nil || out.String() != "no users\n" {
		t.Errorf("empty list: %q, %v", out.String(), err)
	}
}

func TestUserGetHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	u := usersFixture()[0]
	u.Conditions = []*pb.Condition{cond("PasswordSet", condTrue, "Set", "the password is set", time.Hour)}

	var out bytes.Buffer
	if err := renderUserGet(newHuman(&out), &pb.GetUserResponse{User: u}); err != nil {
		t.Fatal(err)
	}

	want := "alice\n" +
		"  Display name Alice\n" +
		"  E-mail       alice@example.com\n" +
		"  Roles        admin\n" +
		"  State        active\n" +
		"  Created      2d ago\n" +
		"\n" +
		"Conditions\n" +
		"  ✓ PasswordSet  Set  the password is set\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// humanUserService answers the calls the write commands make.
type humanUserService struct{ fakeUserService }

func (humanUserService) DeleteUser(context.Context, *connect.Request[pb.DeleteUserRequest]) (*connect.Response[pb.DeleteUserResponse], error) {
	return connect.NewResponse(&pb.DeleteUserResponse{}), nil
}

func (humanUserService) RevokeSessions(context.Context, *connect.Request[pb.RevokeSessionsRequest]) (*connect.Response[pb.RevokeSessionsResponse], error) {
	return connect.NewResponse(&pb.RevokeSessionsResponse{}), nil
}

func (f *humanUserService) GetUser(_ context.Context, r *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.GetUserResponse], error) {
	return connect.NewResponse(&pb.GetUserResponse{User: &pb.User{Name: r.Msg.GetName(), Roles: []string{ichorauth.RoleViewer}}}), nil
}

func newHumanUserClient(t *testing.T, f *humanUserService) ichorv1alpha1connect.UserServiceClient {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(ichorv1alpha1connect.NewUserServiceHandler(f))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return ichorv1alpha1connect.NewUserServiceClient(http.DefaultClient, srv.URL)
}

func resetUserWriteOpts(t *testing.T) {
	t.Helper()

	old := userOpts
	t.Cleanup(func() { userOpts = old })
}

func TestUserCreateHumanOutputPrintsTheGeneratedPasswordOnceAndLast(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	resetUserWriteOpts(t)

	userOpts.generate, userOpts.roles = true, []string{ichorauth.RoleViewer, ichorauth.RoleOperator}

	cmd, out, errw := newUserCmd("")
	if err := runUserCreate(context.Background(), newHumanUserClient(t, &humanUserService{}), cmd, userBob); err != nil {
		t.Fatal(err)
	}

	if want := "✓ created user bob (viewer, operator)\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}

	want := "! Password for bob (shown once, stored nowhere; they must change it at the first sign-in): " + testGenerated + "\n"
	if errw.String() != want {
		t.Errorf("stderr = %q, want %q", errw.String(), want)
	}
}

func TestUserWritesHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	resetUserWriteOpts(t)

	u := newHumanUserClient(t, &humanUserService{})
	ctx := context.Background()

	cmd, out, _ := newUserCmd("")
	cmd.Flags().StringSliceVar(&userOpts.roles, "role", nil, "")

	if err := cmd.Flags().Set("role", "operator"); err != nil {
		t.Fatal(err)
	}

	if err := runUserUpdate(ctx, u, cmd, userBob); err != nil || out.String() != "✓ updated user bob\n" {
		t.Errorf("update: %q, %v", out.String(), err)
	}

	cmd, out, _ = newUserCmd("")
	if err := runUserDelete(ctx, u, cmd, userBob); err != nil || out.String() != "✓ deleted user bob\n" {
		t.Errorf("delete: %q, %v", out.String(), err)
	}

	cmd, out, _ = newUserCmd("")
	if err := runUserRevoke(ctx, u, cmd, userBob); err != nil || out.String() != "✓ revoked the sessions of bob\n" {
		t.Errorf("revoke: %q, %v", out.String(), err)
	}

	userOpts.generate = true

	cmd, out, errw := newUserCmd("")
	if err := runUserSetPassword(ctx, u, cmd, userBob); err != nil {
		t.Fatal(err)
	}

	if want := "✓ set the password of bob (they must change it at the next sign-in)\n"; out.String() != want {
		t.Errorf("set-password stdout = %q, want %q", out.String(), want)
	}

	if strings.Count(errw.String(), testGenerated) != 1 || !strings.Contains(errw.String(), "shown once, stored nowhere; they must change it at the first sign-in): ") {
		t.Errorf("set-password stderr = %q", errw.String())
	}
}

func TestUserMachineOutputIsUnchanged(t *testing.T) {
	for _, format := range []string{outputYAML, outputJSON} {
		setHumanTestFlags(t, format)

		resetUserWriteOpts(t)

		userOpts.generate, userOpts.roles = true, []string{ichorauth.RoleViewer}

		cmd, out, errw := newUserCmd("")
		if err := runUserCreate(context.Background(), newHumanUserClient(t, &humanUserService{}), cmd, userBob); err != nil {
			t.Fatal(err)
		}

		var want bytes.Buffer
		if err := writeMessage(&want, format, &pb.CreateUserResponse{User: &pb.User{Name: userBob, Roles: []string{ichorauth.RoleViewer}}}); err != nil {
			t.Fatal(err)
		}

		if out.String() != want.String() {
			t.Errorf("-o %s stdout:\n%s\nwant:\n%s", format, out.String(), want.String())
		}

		// The password line is what it always was: no mark, no colour.
		if wantErr := generatedPasswordLine(userBob, testGenerated) + "\n"; errw.String() != wantErr || strings.Contains(out.String(), testGenerated) {
			t.Errorf("-o %s stderr = %q", format, errw.String())
		}
	}
}
