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
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// IssuerBackend is also the oidcissuer.RefreshStore: the live
// refresh-token families of a user are kept in the user's entry of the
// credentials Secret, so every replica sees them and RevokeSessions or a
// password change (which rewrite the entry) end them with the entry.
var _ oidcissuer.RefreshStore = (*IssuerBackend)(nil)

// refreshWriteAttempts bounds the optimistic-lock retries of one rotation.
const refreshWriteAttempts = 6

// Start implements oidcissuer.RefreshStore.
func (b *IssuerBackend) Start(ctx context.Context, user, family, jti string, exp time.Time) error {
	return b.updateCredential(ctx, user, func(c *ichorauth.Credential, now time.Time) error {
		for id, f := range c.Families {
			if f.Exp <= now.Unix() {
				delete(c.Families, id)
			}
		}

		if c.Families == nil {
			c.Families = map[string]ichorauth.RefreshFamily{}
		}

		c.Families[family] = ichorauth.RefreshFamily{JTI: jti, Exp: exp.Unix()}

		if len(c.Families) > ichorauth.MaxRefreshFamilies {
			ids := make([]string, 0, len(c.Families))
			for id := range c.Families {
				ids = append(ids, id)
			}

			slices.SortFunc(ids, func(a, b string) int { return cmp.Compare(c.Families[a].Exp, c.Families[b].Exp) })

			for _, id := range ids[:len(c.Families)-ichorauth.MaxRefreshFamilies] {
				delete(c.Families, id)
			}
		}

		return nil
	})
}

// Rotate implements oidcissuer.RefreshStore. A reused token deletes its family
// and the deletion is stored before the error is returned.
func (b *IssuerBackend) Rotate(ctx context.Context, user, family, oldJTI, newJTI string) error {
	return b.updateCredential(ctx, user, func(c *ichorauth.Credential, now time.Time) error {
		f, ok := c.Families[family]
		if !ok || f.Exp <= now.Unix() {
			delete(c.Families, family)

			return oidcissuer.ErrRefreshReused
		}

		if f.JTI != oldJTI {
			delete(c.Families, family)
			slog.Warn("refresh token reused; its family was revoked", "user", user)

			return oidcissuer.ErrRefreshReused
		}

		f.JTI = newJTI
		c.Families[family] = f

		return nil
	})
}

// updateCredential applies fn to the user's entry and stores the result with an
// optimistic lock, retrying on a concurrent write. fn's error is returned after
// the entry was stored.
func (b *IssuerBackend) updateCredential(ctx context.Context, user string, fn func(*ichorauth.Credential, time.Time) error) error {
	if b.Writer == nil {
		return errors.New("refresh tokens cannot be rotated: no writer")
	}

	now := time.Now()
	if b.Now != nil {
		now = b.Now()
	}

	for range refreshWriteAttempts {
		sec, err := b.credentials(ctx)
		if err != nil {
			return errors.New("reading the credentials failed")
		}

		cred, err := ichorauth.ParseCredential(sec.Data[user])
		if err != nil {
			return oidcissuer.ErrRefreshReused // no entry: the user has no valid session
		}

		ferr := fn(&cred, now)

		raw, err := cred.Marshal()
		if err != nil {
			return err
		}

		updated := sec.DeepCopy()
		updated.Data[user] = raw

		err = b.Writer.Patch(ctx, updated, client.MergeFromWithOptions(sec, client.MergeFromWithOptimisticLock{}))
		if err == nil {
			return ferr
		}

		if !apierrors.IsConflict(err) {
			return fmt.Errorf("storing the refresh state failed: %w", err)
		}
	}

	return errors.New("storing the refresh state kept conflicting")
}
