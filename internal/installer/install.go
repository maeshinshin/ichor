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

package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installmanifest"
)

// FieldOwner is the server-side-apply field manager of every write the installer makes.
const FieldOwner = "ichor-installer"

// ReadyFunc waits until obj, just applied, is healthy. The default checks CRDs for
// Established and Deployments for a finished rollout; nothing else has a post-check.
type ReadyFunc func(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error

// Options configures Run.
type Options struct {
	// Namespace is where the snapshot ConfigMaps are written (the installer Job's own).
	Namespace string

	// RunID names the snapshot; the Job passes its own name.
	RunID string

	// Manifest is the install manifest to apply: installmanifest.Manifest in production.
	Manifest []byte

	// Ready overrides the post-check, for tests (envtest runs no Deployment controller).
	Ready ReadyFunc

	// RBACSettle bounds how long an apply retries a Forbidden answer (the authorizer's cache
	// lags the installer's own RBAC write by a moment); zero means 30s.
	RBACSettle time.Duration

	// Timeout bounds each post-check; zero means five minutes.
	Timeout time.Duration

	Log io.Writer
}

// Run applies opts.Manifest. On any failure after the snapshot is taken it restores the
// snapshot and returns an error that says so; the error is never nil unless the new
// manifest is applied and every post-check passed.
func Run(ctx context.Context, c client.Client, opts Options) error {
	if bytes.Contains(opts.Manifest, []byte(installmanifest.PlaceholderSentinel)) {
		return errors.New("this installer binary has no install manifest embedded (built without -tags installembed)")
	}

	if opts.Log == nil {
		opts.Log = io.Discard
	}

	if opts.Ready == nil {
		opts.Ready = waitReady(opts.Timeout)
	}

	objs, err := Decode(opts.Manifest)
	if err != nil {
		return fmt.Errorf("decoding the embedded manifest: %w", err)
	}

	// What init customized is re-derived from the live Deployments and applied to the new
	// manifest's objects, so an upgrade keeps the cluster as init left it.
	objs, err = customizeFromLive(ctx, c, objs)
	if err != nil {
		return fmt.Errorf("carrying init's customizations over to the new manifest: %w", err)
	}

	p := classify(objs)

	// Refusals come before the snapshot and before any write: nothing to roll back.
	if err := checkCRDs(ctx, c, p.crds); err != nil {
		return err
	}

	snap, err := takeSnapshot(ctx, c, opts.Namespace, opts.RunID, p.all())
	if err != nil {
		return err
	}

	logf(opts.Log, "snapshot %s saved: %d objects", opts.RunID, len(snap))

	steps := []struct {
		name string
		objs []*unstructured.Unstructured
	}{{"installer RBAC", p.rbac}, {"CRDs", p.crds}, {"controller", p.controller}, {"UI", p.ui}}

	for i, s := range steps {
		if err := applyStep(ctx, c, opts, s.name, s.objs); err != nil {
			return failWithRollback(c, opts, snap, err)
		}

		// Old snapshots are evidence, not state: failing to remove one must not fail an upgrade.
		// It runs after the installer's own RBAC is applied, because an older release's Role
		// may not allow deleting ConfigMaps.
		if i == 0 {
			if err := pruneSnapshots(ctx, c, opts.Namespace, opts.RunID, SnapshotsKept); err != nil {
				logf(opts.Log, "could not remove old snapshots: %v", err)
			}
		}
	}

	return nil
}

// checkCRDs refuses the whole upgrade when any target CRD would remove something from its
// live counterpart.
func checkCRDs(ctx context.Context, c client.Client, target []*unstructured.Unstructured) error {
	for _, t := range target {
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(t.GroupVersionKind())

		err := c.Get(ctx, types.NamespacedName{Name: t.GetName()}, live)

		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return fmt.Errorf("reading live CRD %s: %w", t.GetName(), err)
		default:
			if err := checkAdditive(live, t); err != nil {
				return fmt.Errorf("refusing to install: %w", err)
			}
		}
	}

	return nil
}

func applyStep(ctx context.Context, c client.Client, opts Options, name string, objs []*unstructured.Unstructured) error {
	logf(opts.Log, "applying %s (%d objects)", name, len(objs))

	for _, o := range objs {
		u := o.DeepCopy()
		u.SetManagedFields(nil)

		if err := applyWithSettle(ctx, c, u, opts.RBACSettle); err != nil {
			return fmt.Errorf("applying %s %s: %w", o.GetKind(), o.GetName(), err)
		}
	}

	for _, o := range objs {
		if err := opts.Ready(ctx, c, o); err != nil {
			return fmt.Errorf("post-check of %s %s: %w", o.GetKind(), o.GetName(), err)
		}
	}

	logf(opts.Log, "%s are ready", name)

	return nil
}

// applyWithSettle applies u, retrying a Forbidden answer for up to settle (default 30s). RBAC
// is evaluated per request against the API server's authorizer cache and the client keeps no
// authorization state (its REST mapper only caches resource kinds), so rights the installer
// just granted itself apply to its next call without a restart; they can lag the write by a
// moment, hence the retry.
func applyWithSettle(ctx context.Context, c client.Client, u *unstructured.Unstructured, settle time.Duration) error {
	if settle <= 0 {
		settle = 30 * time.Second
	}

	var err error

	pollErr := wait.PollUntilContextTimeout(ctx, time.Second, settle, true, func(ctx context.Context) (bool, error) {
		err = applyOne(ctx, c, u)

		return !apierrors.IsForbidden(err), nil
	})
	if err != nil {
		return err
	}

	return pollErr
}

// applyOne server-side applies u, except a Deployment, which is replaced whole. Apply merges
// into fields other managers own (init's Update owns its probes and args), so a tcpSocket
// probe applied over init's httpGet one is two handlers in one probe, which is invalid. The
// replacement keeps the live annotations the Deployment controller writes.
func applyOne(ctx context.Context, c client.Client, u *unstructured.Unstructured) error {
	if u.GetKind() != kindDeployment {
		return c.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), client.FieldOwner(FieldOwner), client.ForceOwnership)
	}

	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(u.GroupVersionKind())

	err := c.Get(ctx, types.NamespacedName{Namespace: u.GetNamespace(), Name: u.GetName()}, live)
	if apierrors.IsNotFound(err) {
		return c.Create(ctx, u, client.FieldOwner(FieldOwner))
	} else if err != nil {
		return err
	}

	repl := u.DeepCopy()
	repl.SetResourceVersion(live.GetResourceVersion())
	repl.SetAnnotations(mergeStrings(live.GetAnnotations(), u.GetAnnotations()))

	return c.Update(ctx, repl, client.FieldOwner(FieldOwner))
}

func mergeStrings(base, over map[string]string) map[string]string {
	out := map[string]string{}
	maps.Copy(out, base)
	maps.Copy(out, over)

	return out
}

// failWithRollback restores snap and returns cause wrapped with how that went. It uses a
// context of its own: the one the step failed under may be the thing that ran out.
func failWithRollback(c client.Client, opts Options, snap []entry, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Minute)
	defer cancel()

	logf(opts.Log, "upgrade failed (%v); rolling back from snapshot %s", cause, opts.RunID)

	if err := rollback(ctx, c, snap); err != nil {
		return fmt.Errorf("%w; rolling back FAILED, the cluster is in a mixed state and snapshot %s holds the previous manifests: %w",
			cause, opts.RunID, err)
	}

	return fmt.Errorf("%w; rolled back to the previous manifests", cause)
}

// rollback writes every snapshot entry back, newest step first (UI, controller, CRDs, installer RBAC). An
// object that did not exist is deleted, except a CRD: deleting one deletes every custom
// resource of that kind, and an additive CRD nothing old reads is harmless.
func rollback(ctx context.Context, c client.Client, snap []entry) error {
	var errs []error

	for _, e := range slices.Backward(snap) {
		live := &unstructured.Unstructured{}
		live.SetAPIVersion(e.APIVersion)
		live.SetKind(e.Kind)

		err := c.Get(ctx, types.NamespacedName{Namespace: e.Namespace, Name: e.Name}, live)
		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("%s %s: %w", e.Kind, e.Name, err))

			continue
		}

		exists := err == nil

		switch {
		case e.Object == nil && exists && e.Kind != kindCRD:
			err = c.Delete(ctx, live)
		case e.Object == nil:
			err = nil
		case !exists:
			err = c.Create(ctx, &unstructured.Unstructured{Object: e.Object})
		default:
			restored := &unstructured.Unstructured{Object: e.Object}
			restored.SetResourceVersion(live.GetResourceVersion())
			err = c.Update(ctx, restored)
		}

		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("%s %s: %w", e.Kind, e.Name, err))
		}
	}

	return errors.Join(errs...)
}

func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, "installer: "+format+"\n", args...)
}

// waitReady is the default post-check.
func waitReady(timeout time.Duration) ReadyFunc {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	return func(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error {
		if obj.GetKind() != kindCRD && obj.GetKind() != kindDeployment {
			return nil
		}

		err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(obj.GroupVersionKind())

			if err := c.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, live); err != nil {
				return false, client.IgnoreNotFound(err)
			}

			if obj.GetKind() == kindCRD {
				return crdEstablished(live), nil
			}

			return rolledOut(live), nil
		})
		if err != nil {
			return fmt.Errorf("not ready within %s: %w", timeout, err)
		}

		return nil
	}
}

func crdEstablished(crd *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
	for _, c := range conds {
		if m, ok := c.(map[string]any); ok && m["type"] == "Established" && m["status"] == "True" {
			return true
		}
	}

	return false
}

// rolledOut is `kubectl rollout status` for a Deployment: the controller has seen the
// latest spec and every replica is updated and available.
func rolledOut(d *unstructured.Unstructured) bool {
	want, found, _ := unstructured.NestedInt64(d.Object, "spec", "replicas")
	if !found {
		want = 1
	}

	observed, _, _ := unstructured.NestedInt64(d.Object, "status", "observedGeneration")
	updated, _, _ := unstructured.NestedInt64(d.Object, "status", "updatedReplicas")
	total, _, _ := unstructured.NestedInt64(d.Object, "status", "replicas")
	available, _, _ := unstructured.NestedInt64(d.Object, "status", "availableReplicas")

	return observed >= d.GetGeneration() && updated == want && total == want && available == want
}
