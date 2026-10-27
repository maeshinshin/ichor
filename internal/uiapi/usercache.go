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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
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
	c.mu.Unlock()
}
