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
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// agentContainerName is the container of the cilium DaemonSet whose image carries the live version.
const agentContainerName = "cilium-agent"

// PreflightInput is what the read-only takeover preconditions need. Nothing it
// does writes to the cluster.
type PreflightInput struct {
	// Kube reads the DaemonSet and the Helm release records.
	Kube kubernetes.Interface
	// Get reads live objects for the diff gate.
	Get LiveGetter
	// IchorNamespace is where Ichor keeps its own release records; Namespaces is where the
	// foreign record is looked for (empty: every namespace).
	IchorNamespace string
	Namespaces     []string
	// Effective are the values Ichor would install with (EffectiveValuesForMode).
	Effective map[string]any
	// UserValues are the operator's --cilium-values for a Cilium with no release record.
	UserValues map[string]any
	// KubeProxy and Tunnel are what the owned-key comparison expects.
	KubeProxy cni.KubeProxyMode
	Tunnel    string
}

// PreflightReport is the outcome of Preflight. Each check records its own result so a plan can
// print all of them; Err folds them into a decision.
type PreflightReport struct {
	// Foreign is nil when there is no foreign release record (case C).
	Foreign *Foreign
	// ForeignErr is a record that cannot be taken over (not deployed, records in two namespaces).
	ForeignErr error
	// Version applies the version rule to the agent image's version.
	Version    VersionCheck
	VersionErr error
	// Conflicts are the Ichor-owned keys whose live value differs; Err gates them.
	Conflicts []Conflict
	// UserValues are the live user values minus the Ichor-owned keys: what becomes
	// spec.cni.cilium.values (case B from the record, case C the operator's own).
	UserValues map[string]any
	// Differences is the diff gate's result against the effective values.
	Differences []Difference
	// CRDsErr is set when the pinned chart ships crds/.
	CRDsErr error
}

// Preflight runs the takeover preconditions read-only and returns the report. An error means a
// check could not run (an API failure), not that a precondition failed.
func Preflight(ctx context.Context, in PreflightInput) (*PreflightReport, error) {
	rep := &PreflightReport{CRDsErr: CheckChartCRDs()}

	foreign, err := ForeignRecords(ctx, in.Kube,
		ForeignOptions{IchorNamespace: in.IchorNamespace, Namespaces: in.Namespaces})
	if err != nil {
		rep.ForeignErr = err
	} else if foreign != nil {
		rep.Foreign = foreign
		rep.ForeignErr = foreign.RequireDeployed()
	}

	live, err := rep.liveValues(in)
	if err != nil {
		return nil, err
	}

	image, err := agentImage(ctx, in.Kube)
	if err != nil {
		return nil, err
	}

	rep.Version, rep.VersionErr = CheckVersion(image)

	if live != nil {
		if rep.Conflicts, err = OwnedKeyConflicts(live, in.KubeProxy, in.Tunnel); err != nil {
			return nil, err
		}
	}

	if rep.Differences, err = DiffLive(ctx, in.Effective, in.Get); err != nil {
		return nil, err
	}

	return rep, nil
}

// liveValues is the complete live configuration and sets r.UserValues; nil when the foreign
// record cannot be read (ForeignErr already says so).
func (r *PreflightReport) liveValues(in PreflightInput) (map[string]any, error) {
	switch {
	case r.Foreign != nil && r.ForeignErr == nil:
		rel := r.Foreign.Latest().Release
		r.UserValues = UserValues(rel.Config)

		return LiveValues(rel)
	case r.Foreign == nil && r.ForeignErr == nil:
		r.UserValues = UserValues(in.UserValues)

		return LiveValuesFromUser(in.UserValues)
	}

	return nil, nil //nolint:nilnil // no readable record: ForeignErr carries the reason
}

func agentImage(ctx context.Context, kube kubernetes.Interface) (string, error) {
	ds, err := kube.AppsV1().DaemonSets(ReleaseNamespace).Get(ctx, ReleaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", errors.New("cilium: no DaemonSet cilium in " + ReleaseNamespace + " to take over")
	}

	if err != nil {
		return "", fmt.Errorf("cilium: reading the cilium DaemonSet: %w", err)
	}

	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == agentContainerName {
			return c.Image, nil
		}
	}

	return "", errors.New("cilium: the cilium DaemonSet has no " + agentContainerName + " container")
}

// Err is nil when the takeover may proceed: every check passed, and the owned-key changes and
// the diff were accepted (--accept-cilium-changes, --accept-cilium-diff).
func (r *PreflightReport) Err(acceptChanges, acceptDiff bool) error {
	errs := []error{r.ForeignErr, r.VersionErr, r.CRDsErr, AcceptConflicts(r.Conflicts, acceptChanges)}

	if len(r.Differences) > 0 && !acceptDiff {
		errs = append(errs, fmt.Errorf("cilium: %d differences between the live objects and the chart; "+
			"review them and pass --accept-cilium-diff", len(r.Differences)))
	}

	return errors.Join(errs...)
}
