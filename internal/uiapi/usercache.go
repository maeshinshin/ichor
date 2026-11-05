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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// UserCacheTTL is how long a read IchorUser may be reused: the bound on how
// stale a disable or a role change can be in the UI.
const UserCacheTTL = 10 * time.Second

// UserCache reads IchorUsers with the ServiceAccount's client and keeps each
// for at most UserCacheTTL. Only found users are cached, so a probe for names
// that do not exist grows nothing.
type UserCache struct {
	Reader    client.Reader
	Namespace string
	// Now is time.Now unless a test sets it.
	Now func() time.Time

	mu sync.Mutex
	m  map[string]cachedUser
	mc map[string]cachedSession
}

// cachedSession is what a token check needs from the credentials entry.
type cachedSession struct {
	mustChange bool
	notBefore  time.Time
	at         time.Time
}

type cachedUser struct {
	u  *ichorv1alpha1.IchorUser
	at time.Time
}

func (c *UserCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}

	return time.Now()
}

// Get returns the user named name. The result is shared: callers must not
// modify it.
func (c *UserCache) Get(ctx context.Context, name string) (*ichorv1alpha1.IchorUser, error) {
	c.mu.Lock()
	e, ok := c.m[name]
	c.mu.Unlock()

	if ok && c.now().Sub(e.at) < UserCacheTTL {
		return e.u, nil
	}

	u := &ichorv1alpha1.IchorUser{}
	if err := c.Reader.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: name}, u); err != nil {
		c.Forget(name)

		return nil, err
	}

	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]cachedUser{}
	}

	c.m[name] = cachedUser{u: u, at: c.now()}
	c.mu.Unlock()

	return u, nil
}

// Forget drops name, so this replica sees a write it just made at once. The
// other replica still waits out its own TTL.
func (c *UserCache) Forget(name string) {
	c.mu.Lock()
	delete(c.m, name)
	delete(c.mc, name)
	c.mu.Unlock()
}

// Session returns name's credentials entry's mustChange and notBefore, cached for at
// most UserCacheTTL like Get: a revoke or password change ends the user's tokens on
// this replica within that time. A user with no entry (or no credentials Secret) is
// not flagged and has a zero notBefore; a read error is returned so callers can fail
// closed.
func (c *UserCache) Session(ctx context.Context, name string) (mustChange bool, notBefore time.Time, err error) {
	c.mu.Lock()
	e, ok := c.mc[name]
	c.mu.Unlock()

	if ok && c.now().Sub(e.at) < UserCacheTTL {
		return e.mustChange, e.notBefore, nil
	}

	var sec corev1.Secret

	err = c.Reader.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: ichorauth.CredentialsSecretName}, &sec)
	if err != nil && !apierrors.IsNotFound(err) {
		return false, time.Time{}, err
	}

	s := cachedSession{at: c.now()}

	if cred, perr := ichorauth.ParseCredential(sec.Data[name]); perr == nil {
		s.mustChange, s.notBefore = cred.MustChange, cred.NotBefore
	}

	c.mu.Lock()
	if c.mc == nil {
		c.mc = map[string]cachedSession{}
	}

	c.mc[name] = s
	c.mu.Unlock()

	return s.mustChange, s.notBefore, nil
}
