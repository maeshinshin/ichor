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
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// The only file in this package that touches pkg/ichorauth's hashing and the
// credentials Secret's layout. The Secret is written by the
// ServiceAccount (s.Client) only after the caller's own RBAC allowed it.

// maxConcurrentHashes: each argon2id hash takes 64 MiB, so at most two run at
// once and the rest wait.
const maxConcurrentHashes = 2

// UserSettings turns Ichor's own users on for a Server.
type UserSettings struct {
	// Cache reads IchorUsers with the ServiceAccount.
	Cache *UserCache
	// IssuerURL is the external URL of ichor-ui and the issuer of its tokens.
	IssuerURL string

	sem chan struct{}
}

// NewUserSettings builds the settings with the hash semaphore ready.
func NewUserSettings(cache *UserCache, issuerURL string) *UserSettings {
	return &UserSettings{Cache: cache, IssuerURL: issuerURL, sem: make(chan struct{}, maxConcurrentHashes)}
}

func (u *UserSettings) acquire(ctx context.Context) (release func(), err error) {
	select {
	case u.sem <- struct{}{}:
		return func() { <-u.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) hashPassword(ctx context.Context, password string) (string, error) {
	release, err := s.Users.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	return ichorauth.HashPassword(password)
}

func (s *Server) verifyPassword(ctx context.Context, password, hash string) (bool, error) {
	release, err := s.Users.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()

	return ichorauth.VerifyPassword(password, hash), nil
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

	if err := json.Unmarshal(raw, &cred); err != nil {
		return cred, false, fmt.Errorf("credentials entry for %q is unreadable", name)
	}

	return cred, true, nil
}

// writeCredential merge-patches one key of the credentials Secret, so two
// replicas never overwrite each other's users. The Secret must exist (the
// chart creates it; the ServiceAccount cannot).
func (s *Server) writeCredential(ctx context.Context, name string, cred ichorauth.Credential) error {
	cred.V = 1

	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}

	patch, err := json.Marshal(map[string]any{"data": map[string][]byte{name: raw}})
	if err != nil {
		return err
	}

	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.ControllerNamespace, Name: ichorauth.CredentialsSecretName}}

	return s.Client.Patch(ctx, sec, client.RawPatch(types.MergePatchType, patch))
}

// setCredential hashes password and stores it for name with the given
// mustChange. notBefore is now: every refresh token issued before this is dead.
func (s *Server) setCredential(ctx context.Context, name, password string, mustChange bool) error {
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return err
	}

	return s.writeCredential(ctx, name, ichorauth.Credential{Hash: hash, NotBefore: s.now().UTC().Truncate(time.Second), MustChange: mustChange})
}

func generatePassword() (string, error) { return ichorauth.GeneratePassword() }

// checkPasswordPolicy applies the password policy and says why it failed.
func checkPasswordPolicy(password string) error {
	if err := ichorauth.ValidatePassword(password); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	return nil
}
