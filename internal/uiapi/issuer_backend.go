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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// IssuerBackend gives the OIDC issuer its users and password checks
// (oidcissuer.UserDirectory and oidcissuer.PasswordVerifier), read with the
// ServiceAccount. A user read is cached for at most UserCacheTTL; the
// credentials entry is read fresh, because it is what revokes sessions.
type IssuerBackend struct {
	Users     *UserCache
	Reader    client.Reader
	Namespace string
	Settings  *UserSettings
}

func (b *IssuerBackend) credentials(ctx context.Context) (*corev1.Secret, error) {
	var sec corev1.Secret

	err := b.Reader.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: ichorauth.CredentialsSecretName}, &sec)
	if apierrors.IsNotFound(err) {
		return &corev1.Secret{}, nil
	}

	return &sec, err
}

// LookupUser implements oidcissuer.UserDirectory.
func (b *IssuerBackend) LookupUser(ctx context.Context, name string) (oidcissuer.User, error) {
	u, err := b.Users.Get(ctx, name)
	if apierrors.IsNotFound(err) {
		return oidcissuer.User{}, oidcissuer.ErrUserNotFound
	}

	if err != nil {
		return oidcissuer.User{}, errors.New("reading the user failed")
	}

	out := oidcissuer.User{Name: u.Name, Roles: u.Spec.Roles, Disabled: u.Spec.Disabled}

	sec, err := b.credentials(ctx)
	if err != nil {
		return oidcissuer.User{}, errors.New("reading the credentials failed")
	}

	if cred, perr := ichorauth.ParseCredential(sec.Data[name]); perr == nil {
		out.NotBefore = cred.NotBefore
	}

	return out, nil
}

// VerifyPassword implements oidcissuer.PasswordVerifier. An unknown or
// disabled user, or one with no password, costs the same as a wrong password:
// it is checked against a dummy hash and answers false.
func (b *IssuerBackend) VerifyPassword(ctx context.Context, name, password string) (bool, error) {
	release, err := b.Settings.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	u, uerr := b.Users.Get(ctx, name)
	if uerr != nil && !apierrors.IsNotFound(uerr) {
		return false, errors.New("reading the user failed")
	}

	sec, err := b.credentials(ctx)
	if err != nil {
		return false, errors.New("reading the credentials failed")
	}

	cred, perr := ichorauth.ParseCredential(sec.Data[name])
	if uerr != nil || u.Spec.Disabled || perr != nil {
		ichorauth.VerifyDummy(password)

		return false, nil
	}

	// ponytail: a hash made with weaker parameters is not upgraded on login (needsRehash is ignored).
	match, _, verr := cred.Verify(password)
	if verr != nil {
		ichorauth.VerifyDummy(password)

		return false, nil
	}

	return match, nil
}
