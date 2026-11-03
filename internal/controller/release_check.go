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
	"net/netip"
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
)

// DefaultReleaseCheckPolicyName is the CiliumNetworkPolicy that lets the controller reach the chart
// repository for the check of newer releases. It exists only while the check is on.
const DefaultReleaseCheckPolicyName = "ichor-controller-release-check"

const (
	// releaseCheckInterval is the time between two checks; each wait is jittered by up to 10 percent.
	releaseCheckInterval = 6 * time.Hour
	// releaseCheckRetry is the wait after a check that could not be recorded (the singleton is absent).
	releaseCheckRetry = time.Minute
)

// ReleaseCheck looks for a newer Ichor release in the chart repository and records the newest one in
// IchorInstallation.status.latestRelease. It runs on the leader only, once when it starts
// and then on an interval. It never changes a spec and never installs anything.
type ReleaseCheck struct {
	// Client writes the status of the IchorInstallation and the egress policy.
	Client client.Client
	// Reader reads the IchorInstallation without the cache, so a check does not wait for an informer.
	Reader client.Reader
	// Settings resolves the switch: spec.updateCheck.enabled, else the --update-check flag.
	Settings *settings.Reader
	// Namespace is the controller's namespace; the egress policy lives there.
	Namespace string
	// Repository is the chart repository whose tags are the releases; List lists them.
	Repository string
	List       releasecheck.Lister
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

		return r.record(ctx, "", nil, true)
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

	tags, err := r.List(r.Repository)
	if err != nil {
		log.Info("Listing the chart repository failed", "repository", r.Repository, "error", err.Error())
	}

	return r.record(ctx, releasecheck.Latest(tags), err, false)
}

// allowRegistry writes the egress rule to the chart repository's host, but only while Ichor's own
// controller policy is installed: a lone egress rule would cut every other flow of the controller. Without that
// policy there is no default deny to open, so the rule is removed.
func (r *ReleaseCheck) allowRegistry(ctx context.Context) (bool, error) {
	app, err := controllerPolicyApp(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName)
	if err != nil {
		return false, err
	}

	if app == "" {
		return false, removePolicy(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName)
	}

	host, err := releasecheck.Host(r.Repository)
	if err != nil {
		return false, err
	}

	return writePolicy(ctx, r.Client, r.Namespace, DefaultReleaseCheckPolicyName, app, releaseCheckSpec(app, host))
}

// releaseCheckSpec is the rule: the controller's pods to the registry host on 443. A host that is an
// IP address is a CIDR; a name is matched with DNS (the static DNS rule carries the L7 part).
func releaseCheckSpec(app, host string) map[string]any {
	rule := map[string]any{keyToPorts: []any{map[string]any{keyPorts: portList(443)}}}

	if a, err := netip.ParseAddr(host); err == nil {
		rule["toCIDRSet"] = []any{map[string]any{keyCIDR: netip.PrefixFrom(a, a.BitLen()).String()}}
	} else {
		rule["toFQDNs"] = []any{map[string]any{keyMatchName: host}}
	}

	return map[string]any{
		"description": "Egress of the controller to the chart repository, for the check of newer Ichor releases " +
			"Written by the controller; edits are overwritten.",
		"endpointSelector": map[string]any{"matchLabels": map[string]any{
			nameLabel: app, "control-plane": valueControllerMgr,
		}},
		keyEgress: []any{rule},
	}
}

// record writes the outcome of a check to status.latestRelease. On an error the last good version
// stays. It reports whether the result was recorded; false means the singleton is absent (yet).
func (r *ReleaseCheck) record(ctx context.Context, found string, checkErr error, cleared bool) bool {
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

	switch {
	case cleared:
		inst.Status.LatestRelease = nil
	default:
		now := metav1.Now()
		rel := &ichorv1alpha1.IchorLatestRelease{CheckedAt: &now}

		if checkErr != nil {
			if inst.Status.LatestRelease != nil {
				rel.Version = inst.Status.LatestRelease.Version
			}

			rel.Error = checkErr.Error()
		} else {
			rel.Version = found
		}

		inst.Status.LatestRelease = rel
	}

	if err := r.Client.Status().Patch(ctx, &inst, client.MergeFrom(orig)); err != nil {
		log.Info("Recording the release check failed", "error", err.Error())

		return false
	}

	return true
}
