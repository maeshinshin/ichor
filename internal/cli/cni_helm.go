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
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// cniHelmCmd groups what only the Cilium Helm Job runs (ADR 0099). Hidden: a person changes
// Cilium by writing spec.cni.cilium.values (or `ichor cni values`), never by running this.
var cniHelmCmd = &cobra.Command{
	Use:    "helm",
	Short:  "Commands the Cilium Helm Job runs",
	Hidden: true,
}

var cniHelmApplyOpts struct {
	Cluster          string
	StorageNamespace string
	KubeProxy        string
	Takeover         bool
	Exclusive        bool
	Timeout          time.Duration
	// ImageVersion and ImageOverrides are a Cilium patch's images (ADR 0115, amendment 2026-11-03), as the
	// controller passes them: each override is key=reference.
	ImageVersion   string
	ImageOverrides []string
}

var cniHelmApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Make Helm release cilium match the TalosCluster's effective Cilium values",
	Long: `apply reads the TalosCluster named by --cluster (in --namespace), computes Cilium's effective
values from it, and runs the pinned chart as Helm release cilium in kube-system: it adopts the
bootstrapped objects into revision 1 when no release exists, and otherwise upgrades (which
prunes, and rolls back on failure). It then waits for the workloads to roll out.

The Job runs it with hostNetwork against KubePrism (localhost:7445), so it keeps API access
while Cilium restarts.

With --takeover (an adopted cluster, ADR 0103) the Cilium it installs over was put there by
someone else: once the release is deployed and the rollout is ready, the foreign Helm release
record in kube-system is copied into the Secret ichor-cilium-prior-release in the storage
namespace, read back, and only then deleted (the record Secrets only, never helm uninstall).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		restCfg, err := loadKubeconfig(kubeconfigPath)
		if err != nil {
			return err
		}

		c, err := newKubeClientForConfig(restCfg)
		if err != nil {
			return err
		}

		log := cmd.ErrOrStderr()

		cfg, err := cilium.NewHelmConfig(restCfg, cniHelmApplyOpts.StorageNamespace, func(format string, v ...any) {
			_, _ = fmt.Fprintf(log, "helm: "+format+"\n", v...)
		})
		if err != nil {
			return err
		}

		var tk *takeoverOptions

		if cniHelmApplyOpts.Takeover {
			kube, err := kubernetes.NewForConfig(restCfg)
			if err != nil {
				return fmt.Errorf("building a Kubernetes client: %w", err)
			}

			tk = &takeoverOptions{Kube: kube, IchorNamespace: cniHelmApplyOpts.StorageNamespace}
		}

		image, err := imageOverrideFromFlags(cniHelmApplyOpts.ImageVersion, cniHelmApplyOpts.ImageOverrides)
		if err != nil {
			return err
		}

		return runCNIHelmApply(cmd.Context(), c, cfg, types.NamespacedName{
			Namespace: apiOpts.Namespace, Name: cniHelmApplyOpts.Cluster,
		}, cniHelmApplyOpts.KubeProxy, cniHelmApplyOpts.Timeout, cniHelmApplyOpts.Exclusive, tk, image, cmd.OutOrStdout())
	},
}

// imageOverrideFromFlags is the Cilium patch the flags name, or nil when they name none (ADR 0115,
// amendment 2026-11-03). A version and its images go together, and each image is key=reference.
func imageOverrideFromFlags(version string, overrides []string) (*ichorv1alpha1.TalosCiliumImageOverride, error) {
	if version == "" && len(overrides) == 0 {
		return nil, nil
	}

	if version == "" || len(overrides) == 0 {
		return nil, errors.New("--image-version and --image-override go together")
	}

	images := make(map[string]string, len(overrides))

	for _, o := range overrides {
		key, ref, ok := strings.Cut(o, "=")
		if !ok || key == "" || ref == "" {
			return nil, fmt.Errorf("--image-override %q is not key=reference", o)
		}

		if _, dup := images[key]; dup {
			return nil, fmt.Errorf("--image-override names the %s image twice", key)
		}

		images[key] = ref
	}

	return &ichorv1alpha1.TalosCiliumImageOverride{Version: version, Images: images}, nil
}

// takeoverOptions switch on the foreign release record's retirement after the rollout (ADR 0103).
type takeoverOptions struct {
	Kube           kubernetes.Interface
	IchorNamespace string
}

// runCNIHelmApply is the command's body, split out so a test can give it a fake client and a
// Helm configuration over the memory driver. takeover is nil unless --takeover.
func runCNIHelmApply(
	ctx context.Context, c client.Client, cfg *action.Configuration, key types.NamespacedName,
	kubeProxy string, timeout time.Duration, exclusive bool, takeover *takeoverOptions,
	image *ichorv1alpha1.TalosCiliumImageOverride, out io.Writer,
) error {
	var cluster ichorv1alpha1.TalosCluster
	if err := c.Get(ctx, key, &cluster); err != nil {
		return fmt.Errorf("reading TalosCluster %s: %w", key, err)
	}

	// The patch the controller names runs for this Job; the values read the cluster's status for it.
	if image != nil {
		if cluster.Status.CNI == nil {
			cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{}
		}

		cluster.Status.CNI.ImageOverride = image
	}

	// The controller names the mode the Job applies (the cluster's current one, or a switch's
	// target); only a hand-run command without --kube-proxy falls back to the spec's.
	if kubeProxy == "" {
		kubeProxy = cluster.Spec.CNI.KubeProxy
	}

	res, err := cilium.ApplyForCluster(ctx, cfg, &cluster, cni.KubeProxyMode(kubeProxy), timeout, exclusive)
	if err != nil {
		return err
	}

	verb := "upgraded"
	if res.Adopted {
		verb = "adopted"
	}

	if _, err = fmt.Fprintf(out, "Helm release %s/%s %s: revision %d, status %s, chart %s\n",
		res.Release.Namespace, res.Release.Name, verb, res.Release.Version, res.Release.Info.Status,
		res.Release.Chart.Metadata.Version); err != nil {
		return err
	}

	if takeover == nil {
		return nil
	}

	return retireForeignRecord(ctx, res, takeover, out)
}

// retireForeignRecord backs up and deletes the foreign Helm record, only once Ichor's own is
// deployed and Apply has waited for the rollout (ADR 0103, 1.2). It prints names, never record data.
func retireForeignRecord(ctx context.Context, res cilium.ApplyResult, tk *takeoverOptions, out io.Writer) error {
	if res.Release.Info == nil || res.Release.Info.Status != release.StatusDeployed {
		return fmt.Errorf("not retiring the foreign Helm release record: Ichor's release %s is not deployed", cilium.ReleaseName)
	}

	rr, err := cilium.RetireForeignRecord(ctx, tk.Kube, cilium.RetireOptions{
		IchorNamespace:             tk.IchorNamespace,
		Namespaces:                 []string{cilium.ReleaseNamespace},
		RemoveForeignReleaseRecord: true,
	})
	if err != nil {
		return err
	}

	if rr.Kept {
		_, err = fmt.Fprintln(out, "no foreign Helm release record to retire")

		return err
	}

	_, err = fmt.Fprintf(out, "foreign Helm release records %s backed up in %s/%s and removed\n",
		strings.Join(rr.Removed, ", "), tk.IchorNamespace, rr.Backup)

	return err
}

func init() {
	cniCmd.AddCommand(cniHelmCmd)
	cniHelmCmd.AddCommand(cniHelmApplyCmd)

	f := cniHelmApplyCmd.Flags()
	f.StringVar(&cniHelmApplyOpts.Cluster, "cluster", "", "name of the TalosCluster whose Cilium values apply (in --namespace)")
	f.StringVar(&cniHelmApplyOpts.StorageNamespace, "storage-namespace", ichorControllerNamespace, "namespace Helm keeps the release record (a Secret) in")
	f.StringVar(&cniHelmApplyOpts.KubeProxy, "kube-proxy", "", "kube-proxy mode to apply (enabled or replaced); default the cluster's spec.cni.kubeProxy")
	f.BoolVar(&cniHelmApplyOpts.Takeover, "takeover", false, "the Cilium being installed over was installed by someone else (an adopted cluster): retire its Helm release record after the rollout")
	f.BoolVar(&cniHelmApplyOpts.Exclusive, "exclusive", false, "no other Helm operation on the release can be running (the controller runs one ichor-cni Job per cluster): repair a pending-* record at once instead of after the timeout")
	f.DurationVar(&cniHelmApplyOpts.Timeout, "timeout", cilium.DefaultApplyTimeout, "how long to wait for Cilium's workloads to roll out")
	f.StringVar(&cniHelmApplyOpts.ImageVersion, "image-version", "", "the Cilium patch (vX.Y.Z) whose images --image-override names; empty for the chart's own images")
	f.StringArrayVar(&cniHelmApplyOpts.ImageOverrides, "image-override", nil, "an image of the Cilium patch as key=repository:tag@sha256:digest (keys: agent, operator, hubble-relay)")
	_ = cniHelmApplyCmd.MarkFlagRequired("cluster")
}
