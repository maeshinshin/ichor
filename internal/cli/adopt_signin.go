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
	"errors"
	"fmt"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
)

// Sign-in is on from the install in adopt as it is in init: ichor-ui is installed with it (applyIchorManifest).
// What is left is set up by init's own functions (initUsers, setClusterAuthentication), after the zero-drift
// plan and the release of the holds: the first administrator, and the authenticator once the nodes run the
// configuration adopt left them with.

// signInPlan is what adopt does to turn Ichor's sign-in on, settled before consent.
type signInPlan struct {
	opts initOptions // UIURL, AdminUser, VersionContract

	// users: the first administrator is created. ichor-ui installed by adopt already runs with sign-in.
	users bool
	// switchUI: an ichor-ui that was installed before and has no sign-in is switched to it.
	switchUI bool
	// authenticator: spec.authentication.ichor is not set on the TalosCluster yet.
	authenticator bool
}

// describe is what the consent prompt and the plan say adopt will do, the apiserver restart included.
func (p *signInPlan) describe() string {
	var parts []string

	if p.users {
		what := fmt.Sprintf("create the first administrator %q (ichor-ui signs in at %s)", p.opts.AdminUser, p.opts.UIURL)
		if p.switchUI {
			what = fmt.Sprintf("create the first administrator %q and switch ichor-ui to sign-in at %s", p.opts.AdminUser, p.opts.UIURL)
		}

		parts = append(parts, what)
	}

	if p.authenticator {
		parts = append(parts, "set spec.authentication.ichor on the TalosCluster, which marks every control plane APIServerRestartRequired: "+
			"kube-apiserver restarts on each control plane, one at a time, when you run `ichor rollout config` "+
			"(until then it runs as it is; with a single control plane the Kubernetes API is unavailable while it restarts)")
	}

	return "set Ichor's sign-in up: " + strings.Join(parts, ", and ")
}

// planSignIn settles, before anything is written, whether adopt turns sign-in on. It validates the
// sign-in flags either way. A Helm-managed Ichor is left alone: its values say whether users are on.
func (st *adoptState) planSignIn(ctx context.Context, deps adoptDeps) error {
	opts, err := prepareInitUsers("adopt", initOptions{
		UIURL: deps.UIURL, AdminUser: deps.AdminUser, VersionContract: st.cluster.Spec.VersionContract,
		UITLSSecret: deps.UITLSSecret, UITLSCert: deps.UITLSCert, UITLSKey: deps.UITLSKey,
		KubeAPIServerURL: deps.KubeAPIServerURL, KubeAPITLSServerName: deps.KubeAPITLSServerName,
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
		if opts.hasUIExtras() {
			return errors.New("adopt: Ichor is Helm-managed, so adopt leaves ichor-ui as the chart has it and cannot apply --ui-tls-* or --kube-api-*: " +
				"set ichorUI.tls.secretName, ichorUI.users.kubeAPIServerURL and ichorUI.users.kubeAPITLSServerName in the chart values instead")
		}

		deps.say(adoptInfo, "Ichor is Helm-managed, so adopt leaves ichor-ui as the chart has it (the chart requires a sign-in) and "+
			"creates no administrator: if you have not, run `ichor create user %s --role admin --generate --bootstrap`, and set "+
			"spec.authentication.ichor (url %s) on the TalosCluster", opts.AdminUser, opts.UIURL)

		return nil
	}

	plan := &signInPlan{opts: opts, users: true}

	if present.Present() {
		ui, err := findDeploymentByLabel(ctx, deps.K8s, ichorControllerNamespace, uiComponentLabel, uiComponentValue)
		if err != nil {
			return err
		}

		plan.users = !installer.ReadUISettings(ui, nil).UsersOn()
		plan.switchUI = plan.users
	}

	// The TLS and kubeconfig flags are applied by the users step; with users already on there is no such step,
	// and a flag that changed nothing would read as done.
	if !plan.users && opts.hasUIExtras() {
		return errors.New("adopt: this Ichor's ichor-ui already has sign-in on, so adopt does not change it and cannot apply " +
			"--ui-tls-* or --kube-api-*: leave them out")
	}

	a := st.cluster.Spec.Authentication
	plan.authenticator = a == nil || a.Ichor == nil || !a.Ichor.Enabled

	if plan.users || plan.authenticator {
		st.signIn = plan
		deps.say(adoptWarn, "after the nodes are released adopt will %s", plan.describe())
	}

	return nil
}

// turnSignInOn is the step after the release of the holds: the first administrator (and, for an Ichor installed
// before, the switch of its ichor-ui), then the authenticator.
// The generated password is kept in st.adminPassword as soon as it exists, so a failure further on
// still shows it.
func (st *adoptState) turnSignInOn(ctx context.Context, deps adoptDeps, clusterName, namespace string) error {
	p := st.signIn

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

// finishSignIn is the end of adopt for sign-in: the step when the holds are all released, a note when they
// are not (a pending takeover), and the generated password when the step fails after the administrator exists.
func (st *adoptState) finishSignIn(ctx context.Context, deps adoptDeps, clusterName, namespace string, complete bool) error {
	if st.signIn == nil {
		return nil
	}

	if !complete {
		deps.say(adoptWarn, "the first administrator is not created yet: run `ichor adopt` again once the Cilium takeover is done; "+
			"until then nobody can sign in to ichor-ui, which runs with sign-in from its install")

		return nil
	}

	if err := st.turnSignInOn(ctx, deps, clusterName, namespace); err != nil {
		// The administrator may exist by now, and nothing else could show its password.
		deps.showAdminPassword(st.signIn.opts.AdminUser, st.adminPassword)
		deps.say(adoptWarn, "adoption stopped before sign-in was on: run the same `ichor adopt` again to continue where it stopped")

		return err
	}

	return nil
}

// showAdminPassword prints the generated password once, verbatim, after everything else adopt printed.
func (d adoptDeps) showAdminPassword(user, password string) {
	if password == "" {
		return
	}

	w := d.Stderr
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
