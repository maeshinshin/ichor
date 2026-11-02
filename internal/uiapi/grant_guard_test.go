package uiapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestUserRoleGrantsStayWithinTheCallersRoles(t *testing.T) {
	s := userServer(t, true, newUser("op", false, "operator"), newUser("vic", false, "viewer"), newUser("boss", false, adminRole))
	op := context.WithValue(context.Background(), identityKey{}, Identity{User: IchorUserPrefix + "op", Source: SourceIchor, Groups: []string{"ichor:operator"}})
	boss := ichorCtx("boss")

	grant := func(ctx context.Context, name string, roles ...string) error {
		_, err := s.UpdateUser(ctx, connect.NewRequest(&pb.UpdateUserRequest{Name: name, Roles: roles}))

		return err
	}

	if err := grant(op, "op", "operator", adminRole); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("an operator made themselves admin: %v", err)
	}

	if err := grant(op, "vic", "viewer", adminRole); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("an operator made someone else admin: %v", err)
	}

	if _, err := s.CreateUser(op, connect.NewRequest(&pb.CreateUserRequest{Name: "new1", Roles: []string{adminRole}})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("an operator created an admin: %v", err)
	}

	if err := grant(op, "vic", "viewer", "operator"); err != nil {
		t.Fatalf("granting a role the caller holds: %v", err)
	}

	if err := grant(boss, "vic", "viewer", adminRole); err != nil {
		t.Fatalf("admin granting admin: %v", err)
	}
}
