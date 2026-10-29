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
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"
	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

const (
	// ichorChartName is the name of Ichor's Helm chart (dist/chart/Chart.yaml).
	ichorChartName = "ichor"
	// ichorChartRef is where the chart is published.
	ichorChartRef = "oci://ghcr.io/maeshinshin/charts/ichor"
)

// ichorPresence is what a cluster says about Ichor being installed: any one of
// the three fields set means Ichor is present.
type ichorPresence struct {
	// Installation is the IchorInstallation singleton (made by `ichor init` or adopt's install).
	Installation *ichorv1alpha1.IchorInstallation
	// Release is a deployed Helm release of the chart ichor.
	Release *release.Release
	// Deployment is a controller Deployment carrying the chart's selector label.
	Deployment *appsv1.Deployment
}

func (p ichorPresence) Present() bool {
	return p.Installation != nil || p.Release != nil || p.Deployment != nil
}

// HelmManaged reports that Helm owns the install. When the IchorInstallation exists its
// spec.deployment.manager is authoritative; before the controller has seeded it, and on
// installs older than that, a deployed release of the chart or a controller Deployment Helm labels
// as its own says so.
func (p ichorPresence) HelmManaged() bool {
	if p.Installation != nil && p.Installation.Spec.Deployment != nil && p.Installation.Spec.Deployment.Manager != "" {
		return p.Installation.HelmManaged()
	}

	return p.Release != nil || (p.Deployment != nil && p.Deployment.Labels["app.kubernetes.io/managed-by"] == "Helm")
}

// ReleaseName is the Helm release's name for a message ("ichor" when only a Deployment shows Helm
// and does not say).
func (p ichorPresence) ReleaseName() string {
	switch {
	case p.Release != nil:
		return p.Release.Name
	case p.Deployment != nil && p.Deployment.Labels["app.kubernetes.io/instance"] != "":
		return p.Deployment.Labels["app.kubernetes.io/instance"]
	}

	return ichorChartName
}

func (p ichorPresence) Namespace() string {
	switch {
	case p.Release != nil:
		return p.Release.Namespace
	case p.Deployment != nil:
		return p.Deployment.Namespace
	}

	return ichorControllerNamespace
}

// Version is the installed Ichor version as far as the cluster says, "" when it does not.
func (p ichorPresence) Version() string {
	switch {
	case p.Release != nil && p.Release.Chart != nil && p.Release.Chart.Metadata != nil && p.Release.Chart.Metadata.AppVersion != "":
		return p.Release.Chart.Metadata.AppVersion
	case p.Installation != nil:
		return p.Installation.Status.InstalledVersion
	}

	return ""
}

// CiliumPoliciesValue is the release's networkPolicy.cilium.enabled as the user set it: nil when
// unset (or when no release record is known).
func (p ichorPresence) CiliumPoliciesValue() *bool {
	if p.Release == nil {
		return nil
	}

	np, _ := p.Release.Config["networkPolicy"].(map[string]any)
	ci, _ := np["cilium"].(map[string]any)

	if b, ok := ci["enabled"].(bool); ok {
		return &b
	}

	return nil
}

// ichorPresent reads, never writes, whether Ichor is installed. A cluster that does not serve a
// kind, or an operator who may not list Secrets or Deployments cluster-wide, simply has no
// evidence from that source.
func ichorPresent(ctx context.Context, c client.Reader) (ichorPresence, error) {
	var p ichorPresence

	var inst ichorv1alpha1.IchorInstallation

	switch err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); {
	case err == nil:
		p.Installation = &inst
	case !absent(err):
		return p, fmt.Errorf("adopt: looking for an existing Ichor installation: %w", err)
	}

	var secrets corev1.SecretList

	switch err := c.List(ctx, &secrets, client.MatchingLabels{"owner": string(provenanceHelm), "status": string(release.StatusDeployed)}); {
	case err == nil:
		for i := range secrets.Items {
			s := &secrets.Items[i]
			if s.Type != "helm.sh/release.v1" {
				continue
			}

			// An undecodable record is someone else's problem, not evidence.
			rel, derr := cilium.DecodeReleaseRecord(s.Data["release"])
			if derr == nil && rel.Chart != nil && rel.Chart.Metadata != nil && rel.Chart.Metadata.Name == ichorChartName {
				p.Release = rel

				break
			}
		}
	case !absent(err) && !apierrors.IsForbidden(err):
		return p, fmt.Errorf("adopt: looking for a Helm release of Ichor: %w", err)
	}

	var deploys appsv1.DeploymentList

	switch err := c.List(ctx, &deploys, client.MatchingLabels{"control-plane": "controller-manager"}); {
	case err == nil:
		if len(deploys.Items) > 0 {
			p.Deployment = &deploys.Items[0]
		}
	case !absent(err) && !apierrors.IsForbidden(err):
		return p, fmt.Errorf("adopt: looking for Ichor's controller Deployment: %w", err)
	}

	return p, nil
}

// absent: the kind is unknown to the cluster or the object is not there.
func absent(err error) bool {
	return apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err)
}

// helmUpgradeCommand is the command that moves a Helm-installed Ichor to version.
func (p ichorPresence) helmUpgradeCommand(version string) string {
	version = cmp.Or(version, "VERSION")

	return fmt.Sprintf("helm upgrade %s %s --version %s --namespace %s", p.ReleaseName(), ichorChartRef, strings.TrimPrefix(version, "v"), p.Namespace())
}

// checkIchorPresence is adopt's first look at an existing Ichor: --require-ichor,
// and, for a Helm-managed install, the version rule.
func checkIchorPresence(ctx context.Context, c client.Reader, w io.Writer, cliVersion string, require bool) error {
	p, err := ichorPresent(ctx, c)
	if err != nil {
		return err
	}

	if !p.Present() {
		if require {
			return errors.New("adopt: --require-ichor: Ichor is not installed in this cluster; install it with Helm first " +
				"(helm install ichor " + ichorChartRef + " --namespace " + ichorControllerNamespace + " --create-namespace)")
		}

		return nil
	}

	if !p.HelmManaged() {
		return nil
	}

	_, _ = fmt.Fprintf(w, "adopt: Ichor is installed by Helm (release %s, version %s); not touching it\n",
		p.ReleaseName(), cmp.Or(p.Version(), "unknown"))

	have, want := semverOf(p.Version()), semverOf(cliVersion)
	if have == "" || want == "" || semver.Compare(have, want) == 0 {
		return nil
	}

	return fmt.Errorf("adopt: Ichor %s is installed by Helm (release %q) but this ichor CLI is %s; they must be equal. Run: %s",
		p.Version(), p.ReleaseName(), cliVersion, p.helmUpgradeCommand(cliVersion))
}

// semverOf is v with its leading "v" made certain, or "" when it is not a release version (a
// "dev" CLI cannot be compared).
func semverOf(v string) string {
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	if semver.IsValid(v) {
		return v
	}

	return ""
}

// refuseHelmManaged is the answer of a command that would run the installer Job on an install
// Helm owns.
func refuseHelmManaged(ctx context.Context, c client.Reader) error {
	p, err := ichorPresent(ctx, c)
	if err != nil || !p.HelmManaged() {
		return err
	}

	return fmt.Errorf("ichor is installed by Helm (release %q, namespace %s); upgrade it with `%s`, not with the installer",
		p.ReleaseName(), p.Namespace(), p.helmUpgradeCommand("VERSION"))
}
