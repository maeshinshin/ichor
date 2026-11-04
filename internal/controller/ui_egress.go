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
	"context"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// DefaultUIEgressPolicyName is the CiliumNetworkPolicy that lets ichor-ui reach its external OIDC issuer
// (ADR 0126). It exists only while the ichor-ui Deployment names an issuer.
const DefaultUIEgressPolicyName = "ichor-ui-oidc-egress"

const (
	uiContainerName = "ichor-ui"
	uiPodLabelValue = "ui-backend"
	envOIDCIssuer   = "ICHOR_OIDC_ISSUER"
	argOIDCIssuer   = "--oidc-issuer="
	// uiEgressInterval is the time between two reads of the Deployment. The controller's Role has no
	// `watch` on Deployments, so a change is picked up on this interval.
	uiEgressInterval = 30 * time.Second
)

// UIEgress keeps the CiliumNetworkPolicy of ichor-ui's egress to its external OIDC issuer current (ADR 0126).
// The issuer is the one the live ichor-ui Deployment is configured with, so the policy cannot drift from what
// ichor-ui dials, on every install path. It writes a Kubernetes object in its own namespace only.
type UIEgress struct {
	// Client reads and writes the policy (unstructured).
	Client client.Client
	// Reader lists the ichor-ui Deployments without the cache (the Role grants no watch).
	Reader client.Reader
	// Namespace is the controller's namespace; the Deployment and the policy live there.
	Namespace string
	// Disabled switches the whole thing off (--no-cilium-policies, --talos-egress-policy=false).
	Disabled bool
	// Settle is how long Start waits after writing, so the agents have the rule before ichor-ui retries.
	Settle time.Duration
	// Interval is the time between two passes; zero means uiEgressInterval.
	Interval time.Duration
}

// NeedLeaderElection makes the policy be written by the leader only.
func (u *UIEgress) NeedLeaderElection() bool { return true }

// Start syncs now and then on the interval, until ctx ends.
func (u *UIEgress) Start(ctx context.Context) error {
	interval := u.Interval
	if interval == 0 {
		interval = uiEgressInterval
	}

	log := logf.FromContext(ctx).WithName("uiegress")

	for {
		changed, err := u.Sync(ctx)

		switch {
		case err != nil:
			log.Info("Keeping ichor-ui's egress policy current failed; will retry", "error", err.Error())
		case changed:
			log.Info("Updated ichor-ui's egress policy", "policy", DefaultUIEgressPolicyName)
			// The pods retry the issuer on their own; give the agents the rule first.
			sleep(ctx, u.Settle)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// +kubebuilder:rbac:groups=apps,namespace=ichor-system,resources=deployments,verbs=get;list

// Sync is the whole job: gate, read the issuer, write or remove the policy. It reports whether it wrote.
// Without Ichor's own controller policy there is no default deny to open, so nothing is written (a lone
// egress rule would itself cut the pods off), and a stale rule is removed.
func (u *UIEgress) Sync(ctx context.Context) (bool, error) {
	if u.Disabled || u.Namespace == "" {
		return false, nil
	}

	app, err := controllerPolicyApp(ctx, u.Client, u.Namespace, DefaultUIEgressPolicyName)
	if err != nil {
		return false, err
	}

	if app == "" {
		return false, removePolicy(ctx, u.Client, u.Namespace, DefaultUIEgressPolicyName)
	}

	target, ok, err := u.issuer(ctx, app)
	if err != nil {
		return false, err
	}

	if !ok {
		return false, removePolicy(ctx, u.Client, u.Namespace, DefaultUIEgressPolicyName)
	}

	return writePolicy(ctx, u.Client, u.Namespace, DefaultUIEgressPolicyName, app, uiEgressSpec(app, target))
}

// issuer is the external OIDC issuer the ichor-ui Deployment is configured with, as a target; false when
// there is none or it is not a URL a rule can name.
func (u *UIEgress) issuer(ctx context.Context, app string) (urlTarget, bool, error) {
	var deployments appsv1.DeploymentList
	if err := u.Reader.List(ctx, &deployments, client.InNamespace(u.Namespace),
		client.MatchingLabels{controlPlaneLabel: uiPodLabelValue, nameLabel: app}); err != nil {
		return urlTarget{}, false, err
	}

	for i := range deployments.Items {
		if t, ok := parseURLTarget(oidcIssuerOf(&deployments.Items[i])); ok {
			return t, true, nil
		}
	}

	return urlTarget{}, false, nil
}

// oidcIssuerOf is the issuer URL of the ichor-ui container: the --oidc-issuer argument, which wins as the
// flag does in cmd/ichor-ui, else ICHOR_OIDC_ISSUER. An environment variable read from a Secret or a
// ConfigMap has no value here and counts as none.
func oidcIssuerOf(dep *appsv1.Deployment) string {
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != uiContainerName {
			continue
		}

		for _, a := range c.Args {
			if v, ok := strings.CutPrefix(a, argOIDCIssuer); ok {
				return v
			}
		}

		for _, e := range c.Env {
			if e.Name == envOIDCIssuer {
				return e.Value
			}
		}
	}

	return ""
}

// uiEgressSpec is the policy: the ichor-ui pods to the issuer on its port. It selects ichor-ui only; the
// controller never dials an IdP.
func uiEgressSpec(app string, issuer urlTarget) map[string]any {
	return map[string]any{
		keyDescription: "Egress of ichor-ui to its external OIDC issuer (ADR 0126). Written by the controller from " +
			"the ichor-ui Deployment's issuer; edits are overwritten.",
		keySelector: map[string]any{keyMatchLabels: map[string]any{
			nameLabel: app, controlPlaneLabel: uiPodLabelValue,
		}},
		keyEgress: []any{issuer.rule()},
	}
}
