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
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/pkg/release"
)

// DefaultReleaseCheckPolicyName is the CiliumNetworkPolicy that lets the controller reach the chart
// repository for the check of newer releases (ADR 0114). It exists only while the check is on.
const DefaultReleaseCheckPolicyName = "ichor-controller-release-check"

const (
	// releaseCheckInterval is the time between two checks; each wait is jittered by up to 10 percent.
	releaseCheckInterval = 6 * time.Hour
	// releaseCheckRetry is the wait after a check that could not be recorded (the singleton is absent).
	releaseCheckRetry = time.Minute
)

// ReleaseCheck looks for a newer Ichor release in the chart repository and the Talos and Kubernetes
// releases in their image repositories (ADR 0114, ADR 0115). The results go to
// IchorInstallation.status.latestRelease and status.upgradeTargets. It runs on the leader only, once
// when it starts and then on an interval. It never changes a spec and never installs anything.
type ReleaseCheck struct {
	// Client writes the status of the IchorInstallation and the egress policy.
	Client client.Client
	// Reader reads the IchorInstallation without the cache, so a check does not wait for an informer.
	Reader client.Reader
	// Settings resolves the switch: spec.updateCheck.enabled, else the --update-check flag.
	Settings *settings.Reader
	// Namespace is the controller's namespace; the egress policy lives there.
	Namespace string
	// Repository is the chart repository whose tags are the Ichor releases; List lists them.
	Repository string
	// TalosRepository and KubernetesRepository are the image repositories whose tags are the Talos
	// and Kubernetes releases (ADR 0115). Empty means the siderolabs default. List lists them too.
	TalosRepository      string
	KubernetesRepository string
	List                 releasecheck.Lister
	// Interval is the time between checks; zero means releaseCheckInterval.
	Interval time.Duration
	// Settle is the wait after the egress policy was written, so the agents have it before the request;
	// main passes 3s, zero in tests.
	Settle time.Duration
}

// NeedLeaderElection makes the check run on the leader only.
func (r *ReleaseCheck) NeedLeaderElection() bool { return true }

// Start checks now and then on the interval, until ctx ends.
func (r *ReleaseCheck) Start(ctx context.Context) error {
	interval := r.Interval
	if interval == 0 {
		interval = releaseCheckInterval
	}

	for {
		next := wait.Jitter(interval, 0.1)
		if !r.check(ctx) {
			next = releaseCheckRetry
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(next):
		}
	}
}

// check runs one pass. It reports false when the result could not be recorded, so the caller retries
// soon; a disabled check counts as done.
func (r *ReleaseCheck) check(ctx context.Context) bool {
	log := logf.FromContext(ctx).WithName("releasecheck")

	eff, err := r.Settings.Effective(ctx)
	if err != nil {
		log.Info("Reading the settings failed; using the flag", "error", err.Error())
	}

	if !eff.UpdateCheck.V {
		if err := removePolicy(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName); err != nil {
			log.Info("Removing the release check's egress policy failed", "error", err.Error())
		}

		return r.record(ctx, func(inst *ichorv1alpha1.IchorInstallation, _ metav1.Time) {
			inst.Status.LatestRelease = nil
			inst.Status.UpgradeTargets = nil
		})
	}

	changed, err := r.allowRegistry(ctx)
	if err != nil {
		log.Info("Writing the release check's egress policy failed; the request may be refused", "error", err.Error())
	}

	if changed {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(r.Settle):
		}
	}

	tags, latestErr := r.List(r.Repository)
	if latestErr != nil {
		log.Info("Listing the chart repository failed", "repository", r.Repository, "error", latestErr.Error())
	}

	talosTags, talosErr := r.List(r.talosRepository())
	if talosErr != nil {
		log.Info("Listing the Talos image repository failed", "repository", r.talosRepository(), "error", talosErr.Error())
	}

	kubeTags, kubeErr := r.List(r.kubernetesRepository())
	if kubeErr != nil {
		log.Info("Listing the Kubernetes image repository failed", "repository", r.kubernetesRepository(), "error", kubeErr.Error())
	}

	return r.record(ctx, func(inst *ichorv1alpha1.IchorInstallation, now metav1.Time) {
		inst.Status.LatestRelease = latestReleaseOf(inst.Status.LatestRelease, releasecheck.Latest(tags), latestErr, now)
		inst.Status.UpgradeTargets = upgradeTargetsOf(inst.Status.UpgradeTargets, now,
			listing{tags: talosTags, err: talosErr}, listing{tags: kubeTags, err: kubeErr})
	})
}

// talosRepository is the image repository of the Talos releases; the default when none is set.
func (r *ReleaseCheck) talosRepository() string {
	if r.TalosRepository == "" {
		return releasecheck.DefaultTalosRepository
	}

	return r.TalosRepository
}

// kubernetesRepository is the image repository of the Kubernetes releases; the default when none is set.
func (r *ReleaseCheck) kubernetesRepository() string {
	if r.KubernetesRepository == "" {
		return releasecheck.DefaultKubernetesRepository
	}

	return r.KubernetesRepository
}

// allowRegistry writes the egress rule to the hosts of the three repositories, but only while Ichor's
// own controller policy is installed: a lone egress rule would cut every other flow of the controller
// (ADR 0100). Without that policy there is no default deny to open, so the rule is removed.
func (r *ReleaseCheck) allowRegistry(ctx context.Context) (bool, error) {
	app, err := controllerPolicyApp(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName)
	if err != nil {
		return false, err
	}

	if app == "" {
		return false, removePolicy(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName)
	}

	hosts, err := r.registryHosts()
	if err != nil {
		return false, err
	}

	return writePolicy(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName, app, releaseCheckSpec(app, hosts))
}

// registryHosts are the distinct registry hosts of the three repositories the check lists.
func (r *ReleaseCheck) registryHosts() ([]string, error) {
	var hosts []string

	for _, repo := range []string{r.Repository, r.talosRepository(), r.kubernetesRepository()} {
		h, err := releasecheck.Host(repo)
		if err != nil {
			return nil, err
		}

		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}

	return hosts, nil
}

// releaseCheckSpec is the rule: the controller's pods to the registry hosts on 443. A host that is an
// IP address is a CIDR; a name is matched with DNS (the static DNS rule carries the L7 part).
func releaseCheckSpec(app string, hosts []string) map[string]any {
	rule := map[string]any{keyToPorts: []any{map[string]any{keyPorts: portList(443)}}}

	var cidrs, names []any

	for _, host := range hosts {
		if a, err := netip.ParseAddr(host); err == nil {
			cidrs = append(cidrs, map[string]any{keyCIDR: netip.PrefixFrom(a, a.BitLen()).String()})
		} else {
			names = append(names, map[string]any{keyMatchName: host})
		}
	}

	if len(cidrs) > 0 {
		rule["toCIDRSet"] = cidrs
	}

	if len(names) > 0 {
		rule["toFQDNs"] = names
	}

	return map[string]any{
		"description": "Egress of the controller to the chart and image repositories, for the check of newer " +
			"Ichor, Talos and Kubernetes releases (ADR 0114, ADR 0115). Written by the controller; edits are overwritten.",
		"endpointSelector": map[string]any{"matchLabels": map[string]any{
			nameLabel: app, "control-plane": valueControllerMgr,
		}},
		keyEgress: []any{rule},
	}
}

// listing is the outcome of listing one image repository.
type listing struct {
	tags []string
	err  error
}

// latestReleaseOf is the new status.latestRelease after a check. On an error the last good version
// stays.
func latestReleaseOf(prev *ichorv1alpha1.IchorLatestRelease, found string, checkErr error, now metav1.Time) *ichorv1alpha1.IchorLatestRelease {
	rel := &ichorv1alpha1.IchorLatestRelease{CheckedAt: &now}

	if checkErr != nil {
		if prev != nil {
			rel.Version = prev.Version
		}

		rel.Error = checkErr.Error()

		return rel
	}

	rel.Version = found

	return rel
}

// upgradeTargetsOf is the new status.upgradeTargets after a listing (ADR 0115): the final releases at or
// above the floors, newest first. A repository that failed keeps its last good list.
func upgradeTargetsOf(prev *ichorv1alpha1.IchorUpgradeTargets, now metav1.Time, talos, kube listing) *ichorv1alpha1.IchorUpgradeTargets {
	out := &ichorv1alpha1.IchorUpgradeTargets{CheckedAt: &now}

	if prev != nil {
		out.Talos, out.Kubernetes = prev.Talos, prev.Kubernetes
	}

	var errs []error

	if talos.err != nil {
		errs = append(errs, fmt.Errorf("listing the Talos releases: %w", talos.err))
	} else {
		out.Talos = release.AtLeast(releasecheck.Finals(talos.tags), release.TalosFloor)
	}

	if kube.err != nil {
		errs = append(errs, fmt.Errorf("listing the Kubernetes releases: %w", kube.err))
	} else {
		out.Kubernetes = release.AtLeast(releasecheck.Finals(kube.tags), release.KubernetesFloor)
	}

	if err := errors.Join(errs...); err != nil {
		out.Error = err.Error()
	}

	return out
}

// record writes the outcome of a check to the IchorInstallation status: update sets the fields it
// owns, from the current status and the check time. It reports whether the result was recorded;
// false means the singleton is absent (yet), or the write failed.
func (r *ReleaseCheck) record(ctx context.Context, update func(inst *ichorv1alpha1.IchorInstallation, now metav1.Time)) bool {
	log := logf.FromContext(ctx).WithName("releasecheck")

	var inst ichorv1alpha1.IchorInstallation
	if err := r.Reader.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		if apierrors.IsNotFound(err) {
			return false
		}

		log.Info("Reading the IchorInstallation failed", "error", err.Error())

		return false
	}

	orig := inst.DeepCopy()

	update(&inst, metav1.Now())

	if err := r.Client.Status().Patch(ctx, &inst, client.MergeFrom(orig)); err != nil {
		log.Info("Recording the release check failed", "error", err.Error())

		return false
	}

	return true
}
