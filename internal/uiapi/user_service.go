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
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// UserService (docs/adr/0094). The IchorUser CR is written as the caller, so
// Kubernetes RBAC decides; the password hash is written by the ServiceAccount
// after a SelfSubjectAccessReview as the caller. Nothing here returns or logs a
// hash, a token or a password (but the generated one, once). Errors about
// credentials say "invalid credentials" and nothing else.

// changePasswordFailures is the per-user back-off of ChangeOwnPassword: the
// same back-off as the issuer's login (oidcissuer.Backoff), so after five
// failures a user waits 1 s, 2 s, 4 s ... up to 15 minutes since the last
// failure. Per replica by design (ADR 0094).
var changePasswordFailures = oidcissuer.NewBackoff()

const (
	kubeAPIPort       = "6443"
	cliClientID       = "ichor-cli"
	kubeAudience      = "ichor-kube"
	adminRole         = "admin"
	passwordSubres    = "password"
	condPasswordSet   = "PasswordSet"
	kubeconfigFileFmt = "ichor-%s.kubeconfig"
	maxCAFileLen      = 4096
)

// requireUsers is the start of every UserService method: users must be on,
// and the call must carry an identity (turning users on turns on login).
func (s *Server) requireUsers(ctx context.Context) (Identity, error) {
	if s.Users == nil {
		return Identity{}, connect.NewError(connect.CodeFailedPrecondition, errors.New("ichor users are not enabled"))
	}

	id, ok := IdentityFrom(ctx)
	if !ok {
		return Identity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in first"))
	}

	return id, nil
}

// mayUpdatePasswords asks Kubernetes, as the caller, whether they hold verb
// update on ichorusers/password. It gates SetPassword and RevokeSessions.
func (s *Server) mayUpdatePasswords(ctx context.Context) error {
	allowed, err := s.may(ctx, verbUpdate, s.ControllerNamespace, "ichorusers", passwordSubres, "")
	if err != nil {
		return err
	}

	if !allowed {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("kubernetes RBAC does not grant you the update verb on ichorusers/password"))
	}

	return nil
}

func userToPB(u *ichorv1alpha1.IchorUser) *pb.User {
	return &pb.User{
		Name: u.Name, DisplayName: u.Spec.DisplayName, Email: u.Spec.Email,
		Roles: u.Spec.Roles, Disabled: u.Spec.Disabled,
		PasswordSet: meta.IsStatusConditionTrue(u.Status.Conditions, condPasswordSet),
		Conditions:  conditions(u.Status.Conditions),
		CreatedAt:   createdAtToPB(u.CreationTimestamp),
	}
}

// invalidArg wraps a validation error as InvalidArgument; nil stays nil.
func invalidArg(err error) error {
	if err == nil {
		return nil
	}

	return connect.NewError(connect.CodeInvalidArgument, err)
}

func (s *Server) getUser(ctx context.Context, name string) (*ichorv1alpha1.IchorUser, error) {
	if err := invalidArg(ichorauth.ValidateUserName(name)); err != nil {
		return nil, err
	}

	var u ichorv1alpha1.IchorUser
	if err := s.Get(ctx, types.NamespacedName{Namespace: s.ControllerNamespace, Name: name}, &u); err != nil {
		return nil, mapGetError(fmt.Sprintf("user %q", name), err)
	}

	return &u, nil
}

// guardGrant refuses a role grant above the caller's own: an Ichor user may add only roles they
// hold, and admin may add any (docs/adr/0108). Kubernetes RBAC alone decided before, so anyone
// allowed to update ichorusers could make themselves admin. An external identity (OIDC) has
// no Ichor roles; for it RBAC remains the only gate. The IchorUser webhook enforces the same rule
// for kubectl (ADR 0129).
func guardGrant(id Identity, current, wanted []string) error {
	if id.Source != SourceIchor {
		return nil
	}

	if r := ichorauth.UngrantedRole(id.Groups, current, wanted); r != "" {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("you cannot grant the role %q: you do not hold it", r))
	}

	return nil
}

func isEnabledAdmin(u *ichorv1alpha1.IchorUser) bool {
	return ichorauth.IsEnabledAdmin(u.Spec.Roles, u.Spec.Disabled)
}

// guardLastAdmin refuses a change that would leave no enabled admin. It is a
// guard against a slip, not a security boundary: the override certificate
// exists (docs/adr/0094).
func (s *Server) guardLastAdmin(ctx context.Context, u *ichorv1alpha1.IchorUser) error {
	if !isEnabledAdmin(u) {
		return nil
	}

	var list ichorv1alpha1.IchorUserList
	if err := s.List(ctx, &list, client.InNamespace(s.ControllerNamespace)); err != nil {
		return mapListError("users", err)
	}

	for i := range list.Items {
		if list.Items[i].Name != u.Name && isEnabledAdmin(&list.Items[i]) {
			return nil
		}
	}

	return connect.NewError(connect.CodeFailedPrecondition, errors.New("this is the last enabled admin; refusing"))
}

func (s *Server) ListUsers(ctx context.Context, _ *connect.Request[pb.ListUsersRequest]) (*connect.Response[pb.ListUsersResponse], error) {
	if _, err := s.requireUsers(ctx); err != nil {
		return nil, err
	}

	var list ichorv1alpha1.IchorUserList
	if err := s.List(ctx, &list, client.InNamespace(s.ControllerNamespace)); err != nil {
		return nil, mapListError("users", err)
	}

	out := &pb.ListUsersResponse{}
	for i := range list.Items {
		out.Users = append(out.Users, userToPB(&list.Items[i]))
	}

	return connect.NewResponse(out), nil
}

func (s *Server) GetUser(ctx context.Context, req *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.GetUserResponse], error) {
	if _, err := s.requireUsers(ctx); err != nil {
		return nil, err
	}

	u, err := s.getUser(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.GetUserResponse{User: userToPB(u)}), nil
}

func (s *Server) CreateUser(ctx context.Context, req *connect.Request[pb.CreateUserRequest]) (*connect.Response[pb.CreateUserResponse], error) {
	id, err := s.requireUsers(ctx)
	if err != nil {
		return nil, err
	}

	m := req.Msg
	if err := invalidArg(errors.Join(ichorauth.ValidateUserName(m.GetName()), ichorauth.ValidateRoles(m.GetRoles()))); err != nil {
		return nil, err
	}

	if err := guardGrant(id, nil, m.GetRoles()); err != nil {
		return nil, err
	}

	u := &ichorv1alpha1.IchorUser{
		Namespace: s.ControllerNamespace, Name: m.GetName(),
		Spec: ichorv1alpha1.IchorUserSpec{DisplayName: m.GetDisplayName(), Email: m.GetEmail(), Roles: m.GetRoles()},
	}

	if err := s.Create(ctx, u); err != nil {
		return nil, userWriteError("creating user", err)
	}

	return connect.NewResponse(&pb.CreateUserResponse{User: userToPB(u)}), nil
}

func (s *Server) UpdateUser(ctx context.Context, req *connect.Request[pb.UpdateUserRequest]) (*connect.Response[pb.UpdateUserResponse], error) {
	id, err := s.requireUsers(ctx)
	if err != nil {
		return nil, err
	}

	m := req.Msg
	if err := invalidArg(ichorauth.ValidateRoles(m.GetRoles())); err != nil {
		return nil, err
	}

	u, err := s.getUser(ctx, m.GetName())
	if err != nil {
		return nil, err
	}

	if err := guardGrant(id, u.Spec.Roles, m.GetRoles()); err != nil {
		return nil, err
	}

	orig := u.DeepCopy()
	u.Spec.DisplayName, u.Spec.Email, u.Spec.Roles, u.Spec.Disabled = m.GetDisplayName(), m.GetEmail(), m.GetRoles(), m.GetDisabled()

	// Demoting or disabling the last enabled admin is refused.
	if !isEnabledAdmin(u) {
		if err := s.guardLastAdmin(ctx, orig); err != nil {
			return nil, err
		}
	}

	if err := s.Update(ctx, u); err != nil {
		return nil, userWriteError("updating user", err)
	}

	s.Users.Cache.Forget(u.Name)

	return connect.NewResponse(&pb.UpdateUserResponse{User: userToPB(u)}), nil
}

func (s *Server) DeleteUser(ctx context.Context, req *connect.Request[pb.DeleteUserRequest]) (*connect.Response[pb.DeleteUserResponse], error) {
	if _, err := s.requireUsers(ctx); err != nil {
		return nil, err
	}

	u, err := s.getUser(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}

	if err := s.guardLastAdmin(ctx, u); err != nil {
		return nil, err
	}

	if err := s.Delete(ctx, u); err != nil {
		return nil, userWriteError("deleting user", err)
	}

	s.Users.Cache.Forget(u.Name)

	return connect.NewResponse(&pb.DeleteUserResponse{}), nil
}

func (s *Server) SetPassword(ctx context.Context, req *connect.Request[pb.SetPasswordRequest]) (*connect.Response[pb.SetPasswordResponse], error) {
	if _, err := s.requireUsers(ctx); err != nil {
		return nil, err
	}

	if err := s.mayUpdatePasswords(ctx); err != nil {
		return nil, err
	}

	u, err := s.getUser(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}

	password, generated := req.Msg.GetPassword(), ""
	if password == "" {
		if password, err = ichorauth.GeneratePassword(); err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("generating a password failed"))
		}

		generated = password
	} else if err := invalidArg(ichorauth.ValidatePassword(password)); err != nil {
		return nil, err
	}

	if err := s.setCredential(ctx, u.Name, password, true); err != nil {
		return nil, credentialWriteError(err)
	}

	return connect.NewResponse(&pb.SetPasswordResponse{GeneratedPassword: generated}), nil
}

func (s *Server) ChangeOwnPassword(ctx context.Context, req *connect.Request[pb.ChangeOwnPasswordRequest]) (*connect.Response[pb.ChangeOwnPasswordResponse], error) {
	id, err := s.requireUsers(ctx)
	if err != nil {
		return nil, err
	}

	name, ok := strings.CutPrefix(id.User, IchorUserPrefix)
	if !ok || id.Source != SourceIchor {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("only an Ichor user has a password here"))
	}

	if err := invalidArg(ichorauth.ValidatePassword(req.Msg.GetNewPassword())); err != nil {
		return nil, err
	}

	cred, found, err := s.readCredential(ctx, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("reading credentials failed"))
	}

	if d := changePasswordFailures.Wait(name, s.now()); d > 0 {
		return nil, connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("too many failed attempts, try again in %d seconds", int(d.Seconds())+1))
	}

	// A missing entry is verified against an empty hash (a dummy inside the
	// verifier), so the cost and the answer do not differ.
	good, err := s.verifyPassword(ctx, req.Msg.GetOldPassword(), cred.Hash)
	if err != nil || !found || !good {
		if err == nil {
			changePasswordFailures.Fail(name, s.now())
		}

		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid credentials"))
	}

	changePasswordFailures.Reset(name)

	if err := s.setCredential(ctx, name, req.Msg.GetNewPassword(), false); err != nil {
		return nil, credentialWriteError(err)
	}

	return connect.NewResponse(&pb.ChangeOwnPasswordResponse{}), nil
}

func (s *Server) RevokeSessions(ctx context.Context, req *connect.Request[pb.RevokeSessionsRequest]) (*connect.Response[pb.RevokeSessionsResponse], error) {
	if _, err := s.requireUsers(ctx); err != nil {
		return nil, err
	}

	if err := s.mayUpdatePasswords(ctx); err != nil {
		return nil, err
	}

	u, err := s.getUser(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}

	cred, found, err := s.readCredential(ctx, u.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("reading credentials failed"))
	}

	if !found {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("this user has no password, so no sessions"))
	}

	cred.NotBefore = s.now().UTC().Truncate(time.Second)
	cred.Families = nil // every refresh token dies with the sessions

	if err := s.writeCredential(ctx, u.Name, cred); err != nil {
		return nil, credentialWriteError(err)
	}

	return connect.NewResponse(&pb.RevokeSessionsResponse{NotBefore: timestamppb.New(cred.NotBefore)}), nil
}

func (s *Server) WhoAmI(ctx context.Context, _ *connect.Request[pb.WhoAmIRequest]) (*connect.Response[pb.WhoAmIResponse], error) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in first"))
	}

	out := &pb.WhoAmIResponse{Username: id.User, Groups: id.Groups, Source: id.Source, DisplayName: id.DisplayName}

	if name, ok := strings.CutPrefix(id.User, IchorUserPrefix); ok && id.Source == SourceIchor && s.Users != nil {
		if cred, found, err := s.readCredential(ctx, name); err == nil && found {
			out.MustChangePassword = cred.MustChange
		}
	}

	return connect.NewResponse(out), nil
}

// GetMyKubeconfig returns public data only: the API server URL, the cluster CA
// certificate and an exec block that runs `ichor auth token`.
func (s *Server) GetMyKubeconfig(ctx context.Context, req *connect.Request[pb.GetMyKubeconfigRequest]) (*connect.Response[pb.GetMyKubeconfigResponse], error) {
	id, err := s.requireUsers(ctx)
	if err != nil {
		return nil, err
	}

	if f := req.Msg.GetCaFile(); len(f) > maxCAFileLen || strings.ContainsFunc(f, unicode.IsControl) {
		return nil, invalid("ca_file is not a usable path")
	}

	name, ok := strings.CutPrefix(id.User, IchorUserPrefix)
	if !ok || id.Source != SourceIchor {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("a kubeconfig that signs in through Ichor is only for an Ichor user"))
	}

	// Reading the cluster as the caller is what requires viewer or better.
	var clusters ichorv1alpha1.TalosClusterList
	if err := s.List(ctx, &clusters, client.InNamespace(s.ControllerNamespace)); err != nil {
		return nil, mapListError("clusters", err)
	}

	if len(clusters.Items) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no cluster"))
	}

	cluster := &clusters.Items[0]

	facts, err := s.publicFacts(ctx, cluster)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("reading the cluster CA failed"))
	}

	server, tlsName := s.Users.kubeServer(cluster.Spec.Endpoint.Address)

	raw, err := buildKubeconfig(cluster.Name, server, tlsName,
		[]byte(facts.KubernetesCA), name, s.Users.IssuerURL, req.Msg.GetCaFile())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("building the kubeconfig failed"))
	}

	return connect.NewResponse(&pb.GetMyKubeconfigResponse{Kubeconfig: string(raw), Filename: fmt.Sprintf(kubeconfigFileFmt, cluster.Name)}), nil
}

// kubeServer is the server (and tls-server-name) of a user's kubeconfig: the
// configured URL when set, else the cluster endpoint on the default port.
func (u *UserSettings) kubeServer(endpoint string) (server, tlsName string) {
	if u.KubeAPIServerURL != "" {
		return u.KubeAPIServerURL, u.KubeAPITLSServerName
	}

	return "https://" + net.JoinHostPort(endpoint, kubeAPIPort), ""
}

// userWriteError is mapGetError for a write the caller's own RBAC may refuse.
func userWriteError(what string, err error) error {
	switch {
	case apierrors.IsForbidden(err):
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("%s: %w", what, err))
	case apierrors.IsAlreadyExists(err):
		return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("%s: %w", what, err))
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return invalid("%s: %w", what, err)
	case apierrors.IsConflict(err):
		return connect.NewError(connect.CodeAborted, fmt.Errorf("%s: %w", what, err))
	default:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("%s: %w", what, err))
	}
}

// credentialWriteError never includes the underlying error: it can quote the
// request, which holds a hash.
func credentialWriteError(err error) error {
	if apierrors.IsNotFound(err) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("the credentials store does not exist; install the chart with ichorUI.users.enabled"))
	}

	return connect.NewError(connect.CodeInternal, errors.New("storing the password failed"))
}

// buildKubeconfig writes the kubeconfig GetMyKubeconfig returns. Its only
// credential is an exec block that runs `ichor auth token`; the CA is public.
func buildKubeconfig(cluster, server, tlsServerName string, ca []byte, user, issuer, caFile string) ([]byte, error) {
	args := []string{"auth", "token", "--issuer", issuer, "--client-id", cliClientID, "--audience", kubeAudience}
	if caFile != "" {
		args = append(args, "--ca-file", caFile)
	}

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[cluster] = &clientcmdapi.Cluster{Server: server, TLSServerName: tlsServerName, CertificateAuthorityData: ca}
	cfg.AuthInfos[user] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion:      "client.authentication.k8s.io/v1",
		Command:         "ichor",
		Args:            args,
		InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
	}}
	cfg.Contexts[cluster] = &clientcmdapi.Context{Cluster: cluster, AuthInfo: user}
	cfg.CurrentContext = cluster

	return clientcmd.Write(*cfg)
}
