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

package clusterlock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// staleReader stands in for a manager cache that has not seen the Lease yet.
type staleReader struct{}

func (staleReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, clusterName)
}

func (staleReader) List(context.Context, client.ObjectList, ...client.ListOption) error { return nil }

// A Renew straight after the Acquire that created the Lease used to read a lagging cache, see
// NotFound and report `held by ""`. Reading through WithReader's live reader sees the holder.
func TestRenew_ReadsThroughTheReader(t *testing.T) {
	ctx, lock := newTestLock(t, time.Minute)

	if err := lock.Acquire(ctx, identityA); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	err := New(k8sClient, lock.key, time.Minute).WithReader(staleReader{}).Renew(ctx, identityA)
	var held *HeldError
	if !errors.As(err, &held) || held.Holder != "" {
		t.Fatalf("Renew through a stale reader = %v, want *HeldError with empty holder", err)
	}
	if strings.Contains(held.Error(), `""`) {
		t.Fatalf("empty-holder message %q should not print an empty quoted holder", held.Error())
	}

	if err := lock.WithReader(k8sClient).Renew(ctx, identityA); err != nil {
		t.Fatalf("Renew through the live reader: %v", err)
	}
}
