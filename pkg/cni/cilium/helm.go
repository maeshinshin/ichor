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

package cilium

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

// DefaultApplyTimeout bounds the wait for the DaemonSet to roll out. Cilium
// restarts one agent per node, so a generous bound beats a rollback of a
// slow but healthy upgrade.
const DefaultApplyTimeout = 10 * time.Minute

// ApplyOptions are Apply's knobs; the zero value is usable.
type ApplyOptions struct {
	// Timeout bounds the wait for the release's workloads (0: DefaultApplyTimeout).
	Timeout time.Duration

	// Exclusive is set by a caller that holds the CNI lock, so no other Helm operation on the
	// release can be running: a pending-* record is then stale whatever its age. Without it a
	// pending record is repaired only once it is older than Timeout.
	Exclusive bool
}

// ApplyResult is what Apply did.
type ApplyResult struct {
	// Release is the stored release after the run.
	Release *release.Release

	// Adopted is true when no release existed and Apply created revision 1 over
	// the bootstrap objects; false when it upgraded an existing release.
	Adopted bool
}

// freshChart loads the pinned chart anew: Helm's actions mutate the chart
// (dependency processing), so the cached copy used for validation is not shared.
func freshChart() (*chart.Chart, error) {
	ch, err := loader.LoadArchive(bytes.NewReader(chartArchive))
	if err != nil {
		return nil, fmt.Errorf("cilium: loading embedded chart: %w", err)
	}

	return ch, nil
}

// Apply makes the Helm release "cilium" in kube-system match vals, the full
// effective values:
//
//   - no release yet: action.Install with TakeOwnership adopts the objects the
//     bootstrap manifest created. The result has no object changes; it records
//     revision 1 and waits for the workloads. No rollback on failure: Atomic
//     here would uninstall a running CNI.
//   - a release: action.Upgrade with the new values. Helm's three-way merge
//     updates changed objects and deletes what the chart no longer renders;
//     Atomic rolls back to the previous revision when the upgrade fails.
//
// cfg carries the storage driver and the Kubernetes client (the CLI builds it
// from a RESTClientGetter; tests use the memory driver and a fake client).
func Apply(ctx context.Context, cfg *action.Configuration, vals map[string]any, o ApplyOptions) (ApplyResult, error) {
	if o.Timeout <= 0 {
		o.Timeout = DefaultApplyTimeout
	}

	ch, err := freshChart()
	if err != nil {
		return ApplyResult{}, err
	}

	last, err := cfg.Releases.Last(ReleaseName)

	switch {
	case errors.Is(err, driver.ErrReleaseNotFound):
		return install(ctx, cfg, ch, vals, o)
	case err != nil:
		return ApplyResult{}, fmt.Errorf("cilium: looking up Helm release %s: %w", ReleaseName, err)
	}

	repaired, err := repairPending(cfg, last, o)
	if err != nil {
		return ApplyResult{}, err
	}

	return upgrade(ctx, cfg, ch, vals, o, repaired)
}

// repairPending is the pending-* repair. A killed install/upgrade/rollback
// leaves the newest record pending-*, and Helm's Upgrade then refuses ("another operation is in
// progress"). When no Helm operation can be running (the caller is Exclusive, or the record is older
// than the wait timeout) the record is marked failed in storage, so the Upgrade that follows goes
// ahead. It never uninstalls. It reports whether it changed a record.
func repairPending(cfg *action.Configuration, last *release.Release, o ApplyOptions) (bool, error) {
	if last == nil || last.Info == nil || !last.Info.Status.IsPending() {
		return false, nil
	}

	if !o.Exclusive && time.Since(last.Info.LastDeployed.Time) < o.Timeout {
		return false, nil
	}

	last.SetStatus(release.StatusFailed, fmt.Sprintf("marked failed by Ichor: a previous operation left it %s", last.Info.Status))

	if err := cfg.Releases.Update(last); err != nil {
		return false, fmt.Errorf("cilium: marking the interrupted Helm revision %d of %s failed: %w", last.Version, ReleaseName, err)
	}

	return true, nil
}

func install(
	ctx context.Context, cfg *action.Configuration, ch *chart.Chart, vals map[string]any, o ApplyOptions,
) (ApplyResult, error) {
	i := action.NewInstall(cfg)
	i.ReleaseName = ReleaseName
	i.Namespace = ReleaseNamespace
	i.TakeOwnership = true
	i.Wait = true
	i.Timeout = o.Timeout
	i.Description = "Adopted by Ichor over the bootstrap manifest"

	rel, err := i.RunWithContext(ctx, ch, vals)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("cilium: adopting the bootstrap objects into Helm release %s: %w", ReleaseName, err)
	}

	return ApplyResult{Release: rel, Adopted: true}, nil
}

func upgrade(
	ctx context.Context, cfg *action.Configuration, ch *chart.Chart, vals map[string]any, o ApplyOptions, takeOwnership bool,
) (ApplyResult, error) {
	u := action.NewUpgrade(cfg)
	// After a repaired install the objects may carry no Helm ownership yet.
	u.TakeOwnership = takeOwnership
	u.Namespace = ReleaseNamespace
	u.Wait = true
	u.Atomic = true
	u.Timeout = o.Timeout
	// The caller always passes the complete effective values; nothing is inherited
	// from the previous revision, so a key a user removed really goes back to default.
	u.ResetValues = true
	u.Description = "Upgraded by Ichor"

	rel, err := u.RunWithContext(ctx, ReleaseName, ch, vals)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("cilium: upgrading Helm release %s: %w", ReleaseName, err)
	}

	return ApplyResult{Release: rel}, nil
}
