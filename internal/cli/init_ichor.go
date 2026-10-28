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

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"golang.org/x/mod/semver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/internal/version"
)

// crdEstablishRetries and crdEstablishInterval bound how long
// applyIchorManifest retries a resource whose kind the API server
// doesn't recognize yet -- expected only for a resource defined by a
// CRD this same manifest just created a moment before, needing a short
// time to become Established.
//
// shortcut: a fixed 10x500ms budget, not a watch on the CRD's
// Established condition. Upgrade to watching Established if a slower
// cluster ever needs longer than 5s here.
const (
	crdEstablishRetries  = 10
	crdEstablishInterval = 500 * time.Millisecond
)

// applyIchorManifest implements the half of design step 3 that no
// longer lives in cluster.inlineManifests: once
// the new cluster's kubeconfig is in hand (design step 4), it applies
// Ichor's own install manifest -- CRDs, RBAC, the controller Deployment
// -- directly through the API server, where a failure comes back as a
// normal, visible error instead of something buried in Talos's boot
// log. manifest is the caller's to supply (production: installmanifest.
// Manifest; tests: a small fixture), so this function never reaches
// into a package-level embed var itself.
//
// It refuses outright if manifest is the placeholder
// (internal/installmanifest's doc comment): that means this binary was
// built with plain `go build`, not `make build-ichor-embed`, and has
// nothing real to apply.
func applyIchorManifest(ctx context.Context, k8sClient client.Client, manifest []byte, warn io.Writer) error {
	if bytes.Contains(manifest, []byte(installmanifest.PlaceholderSentinel)) {
		return fmt.Errorf(
			"init: this ichor binary has no install manifest embedded (built without -tags installembed); " +
				"rebuild with `make build-ichor-embed` before running init")
	}

	objs, err := decodeManifest(manifest)
	if err != nil {
		return fmt.Errorf("init: decoding Ichor's own install manifest: %w", err)
	}

	for _, obj := range objs {
		if err := createWithRetry(ctx, k8sClient, obj, warn); err != nil {
			return fmt.Errorf("init: applying Ichor's own %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	_, _ = fmt.Fprintf(warn, "init: applied Ichor's own install manifest (%d objects)\n", len(objs))

	return nil
}

// decodeManifest splits a multi-document YAML stream into the
// unstructured objects it contains, skipping any empty document (a
// trailing "---" produces one).
func decodeManifest(data []byte) ([]*unstructured.Unstructured, error) {
	return installer.Decode(data)
}

// createWithRetry creates obj, retrying briefly if the API server
// doesn't recognize its kind yet (a CRD this same manifest just created
// needs a moment to become Established), and treating "already exists"
// as success -- re-running init against a cluster that already has some
// of this applied should not fail on that alone.
func createWithRetry(ctx context.Context, k8sClient client.Client, obj *unstructured.Unstructured, progress io.Writer) error {
	err := retryInit(ctx, progress, func() error { return k8sClient.Create(ctx, obj) })
	if apierrors.IsAlreadyExists(err) {
		return nil
	}

	return err
}

// initRetryWindow bounds how long one init apply step keeps retrying; a single control plane
// on a loaded host can answer "etcdserver: request timed out" for a minute after the API
// server first comes up. The delays are followRetryDelay/followRetryMax. A var so tests can
// shrink it.
var initRetryWindow = 3 * time.Minute

// initRetryable is an error init's apply steps wait out: a transient API error, or a kind the
// API server does not know yet (a CRD this install just created is not Established yet).
func initRetryable(err error) bool {
	return isTransient(err) || apimeta.IsNoMatchError(err)
}

// retryInit runs fn, retrying initRetryable errors with backoff for up to initRetryWindow and
// printing one "retrying" line per attempt. Anything else returns at once, as does the last
// error once the window is spent.
func retryInit(ctx context.Context, progress io.Writer, fn func() error) error {
	deadline := time.Now().Add(initRetryWindow)
	delay := followRetryDelay

	for {
		err := fn()
		if err == nil || !initRetryable(err) || time.Now().After(deadline) {
			return err
		}

		_, _ = fmt.Fprintf(progress, "init: retrying in %s: %v\n", delay, err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		delay = min(delay*2, followRetryMax)
	}
}

// createIchorInstallation records the version `ichor init` just installed -- this binary's
// own, since the manifest it applied is embedded in it -- as the IchorInstallation singleton, the CR a later
// TalosOperation of kind ichor reads its target from. A "dev"
// build has no semantic version to record (the CRD would refuse it), so it skips with a
// notice instead of failing the init.
func createIchorInstallation(ctx context.Context, k8sClient client.Client, warn io.Writer) error {
	if !semver.IsValid(version.Version) {
		_, _ = fmt.Fprintf(warn, "init: not creating IchorInstallation: this ichor binary is %q, not a released version\n", version.Version)

		return nil
	}

	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&ichorv1alpha1.IchorInstallation{
		APIVersion: ichorv1alpha1.GroupVersion.String(),
		Kind:       "IchorInstallation",
		Name:       ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			ManagementMode: ichorv1alpha1.IchorManagementModeIchor, Version: version.Version,
		},
	})
	if err != nil {
		return err
	}

	if err := createWithRetry(ctx, k8sClient, &unstructured.Unstructured{Object: content}, warn); err != nil {
		return fmt.Errorf("init: creating the IchorInstallation: %w", err)
	}

	return nil
}
