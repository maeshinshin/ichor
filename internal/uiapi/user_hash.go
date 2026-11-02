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
	"fmt"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// The only file in this package that touches pkg/ichorauth's hashing and the
// credentials Secret's layout (docs/adr/0094). The Secret is written by the
// ServiceAccount (s.Client) only after the caller's own RBAC allowed it.

// Each argon2id hash takes 64 MiB, so at most two run at once (docs/adr/0094). Since ADR 0108
// they are split by who can ask: one slot for the unauthenticated login, with a short queue
// that answers "busy" beyond it, and one for the operations of a signed-in user, so a flood of
// logins cannot starve a password change.
const (
	hashSlotsLogin = 1
	hashSlotsAuthn = 1
	// maxLoginWaiters is how many logins may queue for the login slot before the next one is refused.
	maxLoginWaiters = 8
)

// UserSettings turns Ichor's own users on for a Server.
type UserSettings struct {
	// Cache reads IchorUsers with the ServiceAccount.
	Cache *UserCache
	// IssuerURL is the external URL of ichor-ui and the issuer of its tokens.
	IssuerURL string
	// KubeAPIServerURL, when set, is the server of the kubeconfig GetMyKubeconfig
	// returns instead of the cluster's spec.endpoint; KubeAPITLSServerName, when
	// set, becomes its tls-server-name.
	KubeAPIServerURL, KubeAPITLSServerName string

	sem      chan struct{}
	loginSem chan struct{}
	waiting  atomic.Int32
}

// NewUserSettings builds the settings with the hash semaphore ready.
func NewUserSettings(cache *UserCache, issuerURL string) *UserSettings {
	return &UserSettings{
		Cache: cache, IssuerURL: issuerURL,
		sem: make(chan struct{}, hashSlotsAuthn), loginSem: make(chan struct{}, hashSlotsLogin),
	}
}

// acquireLogin is acquire for the unauthenticated login: at most maxLoginWaiters queue, the
// rest get oidcissuer.ErrBusy at once.
func (u *UserSettings) acquireLogin(ctx context.Context) (release func(), err error) {
	if u.waiting.Add(1) > maxLoginWaiters+hashSlotsLogin {
		u.waiting.Add(-1)

		return nil, oidcissuer.ErrBusy
	}

	select {
	case u.loginSem <- struct{}{}:
		return func() { <-u.loginSem; u.waiting.Add(-1) }, nil
	case <-ctx.Done():
		u.waiting.Add(-1)

		return nil, ctx.Err()
	}
}

func (u *UserSettings) acquire(ctx context.Context) (release func(), err error) {
	select {
	case u.sem <- struct{}{}:
		return func() { <-u.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// verifyPassword checks password against an entry's hash. A missing or
// malformed hash burns the same work against a dummy and answers false.
func (s *Server) verifyPassword(ctx context.Context, password, hash string) (bool, error) {
	release, err := s.Users.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	if hash == "" {
		return ichorauth.VerifyDummy(password), nil
	}

	match, _, err := ichorauth.Verify(password, hash)
	if err != nil {
		return ichorauth.VerifyDummy(password), nil
	}

	return match, nil
}

// readCredential returns the stored entry of user name; found is false when
// the user has no password yet.
func (s *Server) readCredential(ctx context.Context, name string) (cred ichorauth.Credential, found bool, err error) {
	var sec corev1.Secret

	key := types.NamespacedName{Namespace: s.ControllerNamespace, Name: ichorauth.CredentialsSecretName}
	if err := s.Client.Get(ctx, key, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return cred, false, nil
		}

		return cred, false, err
	}

	raw, ok := sec.Data[name]
	if !ok {
		return cred, false, nil
	}

	if cred, err = ichorauth.ParseCredential(raw); err != nil {
		return cred, false, fmt.Errorf("credentials entry for %q is unreadable", name)
	}

	return cred, true, nil
}

// writeCredential merge-patches one key of the credentials Secret, so two
// replicas never overwrite each other's users. The Secret must exist (the
// chart creates it; the ServiceAccount cannot).
func (s *Server) writeCredential(ctx context.Context, name string, cred ichorauth.Credential) error {
	patch, err := cred.MergePatch(name)
	if err != nil {
		return err
	}

	sec := &corev1.Secret{Namespace: s.ControllerNamespace, Name: ichorauth.CredentialsSecretName}

	// Patch is not shadowed by the Server, so this is the ServiceAccount's client.
	return s.Patch(ctx, sec, client.RawPatch(types.MergePatchType, patch))
}

// setCredential hashes password and stores it for name with the given
// mustChange. notBefore is now: every refresh token issued before this is dead.
func (s *Server) setCredential(ctx context.Context, name, password string, mustChange bool) error {
	release, err := s.Users.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	cred, err := ichorauth.NewCredential(password, mustChange, s.now())
	if err != nil {
		return err
	}

	if err := s.writeCredential(ctx, name, cred); err != nil {
		return err
	}

	// This replica must see the new mustChange at once (the other waits its TTL).
	s.Users.Cache.Forget(name)

	return nil
}
