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
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/internal/version"
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
// cilium says Cilium is (or is about to be) this cluster's CNI: init then creates the cilium-secrets
// namespace the manifest's Role and RoleBinding live in; otherwise those two are skipped.
//
// It refuses outright if manifest is the placeholder
// (internal/installmanifest's doc comment): that means this binary was
// built with plain `go build`, not `make build-ichor-embed`, and has
// nothing real to apply.
func applyIchorManifest(ctx context.Context, k8sClient client.Client, manifest []byte, warn io.Writer, cilium bool) error {
	if err := checkInstallManifest(manifest); err != nil {
		return err
	}

	objs, err := installer.Decode(manifest)
	if err != nil {
		return fmt.Errorf("init: decoding Ichor's own install manifest: %w", err)
	}

	// ichor-ui starts with no sign-in here, which serves read-only calls only; init and
	// adopt keep their old behaviour by saying so explicitly. initUsers (--ui-url) turns users on
	// afterwards and CustomizeUI then drops the flag.
	if objs, err = installer.CustomizeObjects(objs, installer.UISettings{AllowUnauthenticated: true}); err != nil {
		return fmt.Errorf("init: preparing Ichor's own install manifest: %w", err)
	}

	for _, obj := range objs {
		inSecrets := obj.GetNamespace() == installer.CiliumSecretsNamespace

		// cilium-secrets comes from Cilium's own bootstrap manifest, which Talos applies minutes after the API
		// server answers, so on a Cilium cluster init creates it first (the Cilium chart's Helm hand-over takes
		// it over, TakeOwnership); anywhere else the objects in it are skipped, as the installer does.
		if inSecrets && cilium {
			ns := &unstructured.Unstructured{}
			ns.SetAPIVersion("v1")
			ns.SetKind("Namespace")
			ns.SetName(installer.CiliumSecretsNamespace)

			if err := createWithRetry(ctx, k8sClient, ns, warn); err != nil {
				return fmt.Errorf("init: creating Namespace %q for Ichor's own %s %q: %w", ns.GetName(), obj.GetKind(), obj.GetName(), err)
			}
		}

		err := createWithRetry(ctx, k8sClient, obj, warn)
		if apierrors.IsNotFound(err) && inSecrets && !cilium {
			_, _ = fmt.Fprintf(warn, "init: skipping %s %s: namespace %s does not exist, Cilium does not run here\n",
				obj.GetKind(), obj.GetName(), installer.CiliumSecretsNamespace)

			continue
		}

		if err != nil {
			return fmt.Errorf("init: applying Ichor's own %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	_, _ = fmt.Fprintf(warn, "init: applied Ichor's own install manifest (%d objects)\n", len(objs))

	return nil
}

// createWithRetry creates obj, retrying briefly if the API server
// doesn't recognize its kind yet (a CRD this same manifest just created
// needs a moment to become Established), and treating "already exists"
// as success -- re-running init against a cluster that already has some
// of this applied should not fail on that alone. It creates with a
// server-side apply as the installer's field manager, so a later `ichor
// upgrade` owns every field it wrote and can remove what a release drops;
// a Deployment is created, as the installer replaces those whole.
func createWithRetry(ctx context.Context, k8sClient client.Client, obj *unstructured.Unstructured, progress io.Writer) error {
	err := retryInit(ctx, progress, func() error {
		if obj.GetKind() == "Deployment" {
			return k8sClient.Create(ctx, obj, client.FieldOwner(installer.FieldOwner))
		}

		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(obj.GroupVersionKind())

		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), live); err == nil {
			return apierrors.NewAlreadyExists(schema.GroupResource{Resource: obj.GetKind()}, obj.GetName())
		} else if !apierrors.IsNotFound(err) {
			return err
		}

		return k8sClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), client.FieldOwner(installer.FieldOwner), client.ForceOwnership)
	})
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
	return isTransient(err) || apimeta.IsNoMatchError(err) || webhookRefused(err)
}

// webhookRefused is the API server failing to call one of Ichor's admission webhooks.
func webhookRefused(err error) bool {
	return apierrors.IsInternalError(err) && strings.Contains(err.Error(), "webhook")
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

		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}

		delay = min(delay*2, followRetryMax)
	}
}

// createRetrying creates obj through retryInit. A Create that timed out may still have gone
// through, so AlreadyExists after a retry counts as success; on the first try it is returned.
func createRetrying(ctx context.Context, c client.Client, obj client.Object, progress io.Writer) error {
	tries := 0

	return retryInit(ctx, progress, func() error {
		tries++

		if err := c.Create(ctx, obj); !apierrors.IsAlreadyExists(err) || tries == 1 {
			return err
		}

		return nil
	})
}

// updateRetrying updates obj through retryInit. A Conflict (another writer bumped the
// resourceVersion: an Update that timed out and landed, or the Deployment controller writing
// status) re-Gets obj, redoes mutate on it and updates again; init owns these objects.
func updateRetrying[T client.Object](ctx context.Context, c client.Client, obj T, mutate func(T) error, progress io.Writer) error {
	return retryInit(ctx, progress, func() error {
		err := c.Update(ctx, obj)

		for range 5 {
			if !apierrors.IsConflict(err) {
				return err
			}

			if err = c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				return err
			}

			if err = mutate(obj); err != nil {
				return err
			}

			err = c.Update(ctx, obj)
		}

		return err
	})
}

// installerPin is what init records about the installer image in the IchorInstallation: the digest
// to pin (spec.installerDigest; empty leaves it to the controller to resolve the tag) and whether the
// signature check is skipped (spec.installerSignature.mode skip)..
type installerPin struct {
	Digest        string
	SkipSignature bool
}

// validateInitInstaller refuses an --installer-digest the IchorInstallation would refuse, before anything is changed.
// checkInstallManifest refuses a binary with no install manifest embedded. A dry run asks too, since a
// real run would only learn it after the node is configured.
func checkInstallManifest(manifest []byte) error {
	if bytes.Contains(manifest, []byte(installmanifest.PlaceholderSentinel)) {
		return fmt.Errorf(
			"init: this ichor binary has no install manifest embedded (built without -tags installembed); " +
				"rebuild with `make build-ichor-embed` before running init")
	}

	return nil
}

func validateInitInstaller(opts initOptions) error {
	if opts.InstallerDigest != "" && !installerDigestPattern.MatchString(opts.InstallerDigest) {
		return fmt.Errorf("init: --installer-digest %q is not sha256: followed by 64 lowercase hex characters", opts.InstallerDigest)
	}

	return nil
}

// createIchorInstallation records the version `ichor init` just installed -- this binary's
// own, since the manifest it applied is embedded in it -- as the IchorInstallation singleton, the CR a later
// TalosOperation of kind ichor reads its target from. A "dev"
// build has no semantic version to record (the CRD would refuse it), so it skips with a
// notice instead of failing the init.
//
// The bool is false when nothing was created (a dev build), in which case there is no Cilium
// hand-over to wait for.
func createIchorInstallation(ctx context.Context, k8sClient client.Client, warn io.Writer, wait time.Duration, pin installerPin) (bool, error) {
	if !semver.IsValid(version.Version) {
		_, _ = fmt.Fprintf(warn, "init: not creating IchorInstallation: this ichor binary is %q, not a released version\n", version.Version)

		return false, nil
	}

	spec := ichorv1alpha1.IchorInstallationSpec{
		Version:         version.Version,
		InstallerDigest: pin.Digest,
		Deployment:      &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerInstaller},
	}

	if pin.SkipSignature {
		spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip}

		_, _ = fmt.Fprintln(warn, "init: Warning: the signature of the installer image is NOT checked "+
			"(spec.installerSignature.mode skip); the next `ichor upgrade ichor` without --skip-signature-check checks it again")
	}

	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&ichorv1alpha1.IchorInstallation{
		APIVersion: ichorv1alpha1.GroupVersion.String(),
		Kind:       "IchorInstallation",
		Name:       ichorv1alpha1.IchorInstallationName,
		Spec:       spec,
	})
	if err != nil {
		return false, err
	}

	// The IchorInstallation has a validating webhook, served by the controller this
	// command just deployed: wait for it as long as step 5 waits for the CRs, not initRetryWindow.
	if err := createWhenWebhooksUp(ctx, k8sClient, &unstructured.Unstructured{Object: content}, cmp.Or(wait, defaultWebhookWait), followRetryDelay, warn); err != nil {
		return false, fmt.Errorf("init: creating the IchorInstallation: %w", err)
	}

	return true, nil
}
