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
	"fmt"
	"io"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
)

// Sign-in is on by default in adopt as it is in init, and it is set up by init's own functions
// (initUsers, setClusterAuthentication), after the zero-drift plan and the release of the holds:
// the authenticator goes in once the nodes run the configuration adopt left them with.

// signInPlan is what adopt does to turn Ichor's sign-in on, settled before consent.
type signInPlan struct {
	opts initOptions // UIURL, AdminUser, VersionContract

	// users: ichor-ui is not at sign-in yet, so the first administrator is created and ichor-ui switched.
	users bool
	// authenticator: spec.authentication.ichor is not set on the TalosCluster yet.
	authenticator bool
}

// describe is what the consent prompt and the plan say adopt will do, the apiserver restart included.
func (p *signInPlan) describe() string {
	var parts []string

	if p.users {
		parts = append(parts, fmt.Sprintf("create the first administrator %q and switch ichor-ui to sign-in at %s", p.opts.AdminUser, p.opts.UIURL))
	}

	if p.authenticator {
		parts = append(parts, "set spec.authentication.ichor on the TalosCluster, which marks every control plane APIServerRestartRequired: "+
			"kube-apiserver restarts on each control plane, one at a time, when you run `ichor rollout config` "+
			"(until then it runs as it is; with a single control plane the Kubernetes API is unavailable while it restarts)")
	}

	return "turn Ichor's sign-in on: " + strings.Join(parts, ", and ")
}

// planSignIn settles, before anything is written, whether adopt turns sign-in on. It validates the
// sign-in flags either way. A Helm-managed Ichor is left alone: its values say whether users are on.
func (st *adoptState) planSignIn(ctx context.Context, deps adoptDeps) error {
	opts, err := prepareInitUsers("adopt", initOptions{
		UIURL: deps.UIURL, AdminUser: deps.AdminUser, AllowUnauthenticated: deps.AllowUnauthenticated,
		VersionContract: st.cluster.Spec.VersionContract,
	})
	if err != nil || st.adding || deps.Ichor == nil {
		return err
	}

	present, err := ichorPresent(ctx, deps.K8s)
	if err != nil {
		return err
	}

	// shortcut: a Helm-managed Ichor is not touched, so adopt neither switches its ichor-ui nor
	// sets spec.authentication.ichor on its cluster; drop when adopt can read the chart's values.
	if present.HelmManaged() {
		if opts.signInOn() {
			deps.say(adoptInfo, "Ichor is Helm-managed, so adopt leaves ichor-ui as the chart has it; turn sign-in on with "+
				"ichorUI.users.enabled=true and ichorUI.users.externalURL=%s", opts.UIURL)
		}

		return nil
	}

	st.openUI = deps.AllowUnauthenticated

	if !opts.signInOn() {
		return nil
	}

	plan := &signInPlan{opts: opts, users: true}

	if present.Present() {
		ui, err := findDeploymentByLabel(ctx, deps.K8s, ichorControllerNamespace, uiComponentLabel, uiComponentValue)
		if err != nil {
			return err
		}

		plan.users = !installer.ReadUISettings(ui, nil).UsersOn()
	}

	a := st.cluster.Spec.Authentication
	plan.authenticator = a == nil || a.Ichor == nil || !a.Ichor.Enabled

	if plan.users || plan.authenticator {
		st.signIn = plan
		deps.say(adoptWarn, "after the nodes are released adopt will %s", plan.describe())
	}

	return nil
}

// turnSignInOn is the step after the release of the holds: the users step, then the authenticator.
// The generated password is kept in st.adminPassword as soon as it exists, so a failure further on
// still shows it.
func (st *adoptState) turnSignInOn(ctx context.Context, deps adoptDeps, clusterName, namespace string) error {
	p := st.signIn
	if p == nil {
		return nil
	}

	if p.users {
		err := deps.run("Ichor users", func(d adoptDeps) (string, error) {
			var err error

			st.adminPassword, err = initUsers(ctx, p.opts, d.K8s, d.Stderr)

			return "", err
		})
		if err != nil {
			return err
		}
	}

	if !p.authenticator {
		return nil
	}

	cluster := &ichorv1alpha1.TalosCluster{Namespace: namespace, Name: clusterName}

	return deps.run("Authentication", func(d adoptDeps) (string, error) {
		return "spec.authentication.ichor is set", setClusterAuthentication(ctx, d.K8s, cluster, p.opts.UIURL, d.Stderr)
	})
}

// showAdminPassword prints the generated password once, verbatim, after everything else adopt printed.
func (d adoptDeps) showAdminPassword(user, password string) {
	if password == "" {
		return
	}

	var w io.Writer = d.Stderr
	if d.Steps != nil {
		w = d.Steps
	}

	printGeneratedPassword(w, user, password)
}

// signInEpilogue is what the operator does after a sign-in adopt: where ichor-ui must answer, the
// restart that waits for them, and the password. The password comes last.
func (d adoptDeps) signInEpilogue(st *adoptState) {
	p := st.signIn
	if p == nil {
		return
	}

	if p.opts.UIURL != defaultUIURL {
		d.say(adoptWarn, "%s", uiReachNotice(p.opts.UIURL))
	}

	if p.authenticator {
		d.say(adoptWarn, "%s", authRolloutNotice)
	}

	d.showAdminPassword(p.opts.AdminUser, st.adminPassword)
}
