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
	"log/slog"
	"time"

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
	// Writer, when set, upgrades a hash made with weaker parameters after a
	// successful login. nil leaves old hashes as they are.
	Writer client.Writer
	// Now is the clock; nil means time.Now.
	Now func() time.Time
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
		out.MustChange = cred.MustChange
	}

	return out, nil
}

// VerifyPassword implements oidcissuer.PasswordVerifier. An unknown or
// disabled user, or one with no password, costs the same as a wrong password:
// it is checked against a dummy hash and answers false.
func (b *IssuerBackend) VerifyPassword(ctx context.Context, name, password string) (bool, error) {
	release, err := b.Settings.acquireLogin(ctx)
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

	match, needsRehash, verr := cred.Verify(password)
	if verr != nil {
		ichorauth.VerifyDummy(password)

		return false, nil
	}

	if match && needsRehash {
		b.rehash(ctx, sec, name, cred, password)
	}

	return match, nil
}

// rehash stores a fresh hash of a password that just verified against a
// weaker one, keeping notBefore and mustChange. It patches only this user's
// key and fails on a changed Secret, so a concurrent password change is never
// overwritten. A failure is logged (never the hash or password) and does not
// block the login.
func (b *IssuerBackend) rehash(ctx context.Context, sec *corev1.Secret, name string, cred ichorauth.Credential, password string) {
	if b.Writer == nil {
		return
	}

	fail := func(err error) { slog.Warn("upgrading a password hash failed", "user", name, "error", err.Error()) }

	var err error
	if cred.Hash, err = ichorauth.Hash(password); err != nil {
		fail(err)

		return
	}

	raw, err := cred.Marshal()
	if err != nil {
		fail(err)

		return
	}

	updated := sec.DeepCopy()
	updated.Data[name] = raw

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := b.Writer.Patch(ctx, updated, client.MergeFromWithOptions(sec, client.MergeFromWithOptimisticLock{})); err != nil {
		fail(err)
	}
}
