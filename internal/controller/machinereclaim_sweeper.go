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

package controller

import (
	"cmp"
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ReclaimCredentialSweeper deletes every override credential Secret older than the maximum TTL
// (ADR 0103 section 2.2). It does not look at any MachineReclaim, so a lost or stuck request
// cannot leave a foreign credential behind, and it runs whether or not reclaim is enabled.
type ReclaimCredentialSweeper struct {
	client.Client
	// Namespace is where credentials live. Empty means all namespaces.
	Namespace string
	// MaxAge defaults to ReclaimMaxCredentialAge, Interval to one minute, Now to time.Now.
	MaxAge   time.Duration
	Interval time.Duration
	Now      func() time.Time
}

// NeedLeaderElection makes one replica sweep.
func (*ReclaimCredentialSweeper) NeedLeaderElection() bool { return true }

// Start sweeps once a minute until ctx ends.
func (s *ReclaimCredentialSweeper) Start(ctx context.Context) error {
	t := time.NewTicker(cmp.Or(s.Interval, time.Minute))
	defer t.Stop()

	for {
		if _, err := s.Sweep(ctx); err != nil {
			logf.FromContext(ctx).Info("sweeping reclaim credentials failed; will retry", "error", err.Error())
		}

		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Sweep deletes the overdue credentials and returns how many it deleted. It selects by the
// ichor.maesh.dev/reclaim label and re-checks the Secret type, so it cannot touch another Secret.
func (s *ReclaimCredentialSweeper) Sweep(ctx context.Context) (int, error) {
	now := nowFrom(s.Now)

	var list corev1.SecretList

	opts := []client.ListOption{client.HasLabels{ichorv1alpha1.ReclaimLabel}}
	if s.Namespace != "" {
		opts = append(opts, client.InNamespace(s.Namespace))
	}

	if err := s.List(ctx, &list, opts...); err != nil {
		return 0, err
	}

	deleted := 0

	for i := range list.Items {
		sec := &list.Items[i]
		if sec.Type != ichorv1alpha1.ReclaimCredentialSecretType || now.Sub(sec.CreationTimestamp.Time) <= cmp.Or(s.MaxAge, ReclaimMaxCredentialAge) {
			continue
		}

		if err := s.Delete(ctx, sec, client.Preconditions{UID: &sec.UID}); client.IgnoreNotFound(err) != nil {
			return deleted, err
		}

		deleted++

		logf.FromContext(ctx).Info("deleted an overdue reclaim credential", "secret", sec.Name)
	}

	return deleted, nil
}
