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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// This file writes an IchorUser and its password hash straight through the
// Kubernetes API with the caller's own rights ("First admin"):
// `ichor init --admin-user` uses it on a cluster that has no login yet, and
// `ichor user create --bootstrap` uses it to make the first admin of an
// existing install. It hashes locally with pkg/ichorauth, the package ichor-ui
// uses, so the entry is the one ichor-ui reads. The password never reaches a
// log, a file or an error message.

// userSpec is what a new user carries; the password is separate.
type userSpec struct {
	Name, DisplayName, Email string
	Roles                    []string
}

func runUserBootstrap(cmd *cobra.Command, name string) error {
	password, err := bootstrapPassword(cmd, userOpts.passwordStdin, userOpts.generate)
	if err != nil {
		return err
	}

	k, err := newKubeClient(kubeconfigPath)
	if err != nil {
		return err
	}

	spec := userSpec{Name: name, DisplayName: userOpts.displayName, Email: userOpts.email, Roles: userOpts.roles}
	if err := createUserWithPassword(cmd.Context(), k, ichorControllerNamespace, spec, password, time.Now()); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Created user %s with the password hash in %s/%s (must be changed at the first sign-in).\n",
		name, ichorControllerNamespace, ichorauth.CredentialsSecretName)

	if userOpts.generate {
		printGeneratedPassword(cmd.ErrOrStderr(), name, password)
	}

	return nil
}

// bootstrapPassword is resolvePassword for a path that hashes locally: a
// generated password is made here instead of by ichor-ui.
func bootstrapPassword(cmd *cobra.Command, fromStdin, generate bool) (string, error) {
	pw, err := resolvePassword(cmd, fromStdin, generate)
	if err != nil {
		return "", err
	}

	if generate {
		return ichorauth.GeneratePassword()
	}

	return pw, nil
}

// createUserWithPassword creates the IchorUser CR and then its credentials
// entry with mustChange set. It refuses an existing user, so it can never reset
// somebody's password. The credentials Secret must exist (the chart makes it
// with ichorUI.users.enabled; `ichor init --ui-url` makes it itself).
func createUserWithPassword(ctx context.Context, k client.Client, ns string, spec userSpec, password string, now time.Time) error {
	if err := errors.Join(ichorauth.ValidateUserName(spec.Name), validateRoleNames(spec.Roles), ichorauth.ValidatePassword(password)); err != nil {
		return err
	}

	// Hash first: a bad password leaves nothing behind.
	cred, err := ichorauth.NewCredential(password, true, now)
	if err != nil {
		return fmt.Errorf("hashing the password: %w", err)
	}

	var sec corev1.Secret
	if err := k.Get(ctx, types.NamespacedName{Namespace: ns, Name: ichorauth.CredentialsSecretName}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("the Secret %s/%s does not exist: users are not turned on in this install "+
				"(set ichorUI.users.enabled in the chart, or use `ichor init --ui-url`)", ns, ichorauth.CredentialsSecretName)
		}

		return fmt.Errorf("reading the credentials Secret: %w", err)
	}

	u := &ichorv1alpha1.IchorUser{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: spec.Name},
		Spec:       ichorv1alpha1.IchorUserSpec{DisplayName: spec.DisplayName, Email: spec.Email, Roles: spec.Roles},
	}

	if err := retryNoMatch(ctx, func() error { return k.Create(ctx, u) }); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("user %q already exists; use `ichor user set-password` (as an admin) to change its password", spec.Name)
		}

		return fmt.Errorf("creating the IchorUser: %w", err)
	}

	if err := patchCredential(ctx, k, ns, spec.Name, cred); err != nil {
		return fmt.Errorf("user %q was created but storing its password failed: %w", spec.Name, err)
	}

	return nil
}

func validateRoleNames(roles []string) error {
	if len(roles) > ichorauth.MaxRoles {
		return fmt.Errorf("at most %d roles", ichorauth.MaxRoles)
	}

	var errs []error
	for _, r := range roles {
		errs = append(errs, ichorauth.ValidateRole(r))
	}

	return errors.Join(errs...)
}

// patchCredential merge-patches one key of the credentials Secret, the way
// ichor-ui does, so it never overwrites another user's entry.
func patchCredential(ctx context.Context, k client.Client, ns, name string, cred ichorauth.Credential) error {
	raw, err := cred.Marshal()
	if err != nil {
		return err
	}

	patch, err := json.Marshal(map[string]any{"data": map[string][]byte{name: raw}})
	if err != nil {
		return err
	}

	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: ichorauth.CredentialsSecretName}}

	// The error of a failed patch can quote the request, which holds a hash.
	if err := k.Patch(ctx, sec, client.RawPatch(types.MergePatchType, patch)); err != nil {
		return errors.New("the Kubernetes API refused the patch (do you have update on the Secret?)")
	}

	return nil
}

// retryNoMatch retries fn while the API server does not know the kind yet (a
// CRD this install just created needs a moment to become Established).
func retryNoMatch(ctx context.Context, fn func() error) error {
	var err error

	for range crdEstablishRetries {
		if err = fn(); !apimeta.IsNoMatchError(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(crdEstablishInterval):
		}
	}

	return err
}
