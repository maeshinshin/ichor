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
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

var cniBootstrapOpts struct {
	KubeProxy    string
	Tunnel       string
	CiliumValues string
}

var bootstrapCniCmd = &cobra.Command{
	Use:   "cni --kube-proxy enabled|replaced",
	Short: "Install Ichor's Cilium on a cluster with no CNI, before helm install ichor",
	Long: `bootstrap cni is the first step of the Helm path on a cluster with no CNI: such a
cluster cannot run any pod, so Ichor's chart cannot start. bootstrap installs Ichor's pinned Cilium
as Ichor's own Helm release (release cilium in kube-system, its record in ichor-system) with the
operator's kubeconfig, and waits for every node to be Ready. Then run helm install ichor, then
ichor adopt, which recognises this Cilium as Ichor's and does not take it over.

It refuses unless the cluster has no CNI: no CNI workload, no CiliumNode CRD, and every node
NotReady with the network-plugin message. It has no Talos credential, so it takes Talos's
cluster.network.cni.name to be none (the only way a cluster ends up like this) and reads nothing
else from Talos: --kube-proxy says whether Cilium replaces kube-proxy. Replaced: kube-proxy is
disabled in Talos, and Cilium reaches the API through KubePrism on port 7445. Enabled: Cilium's
API address is the chart's default. In both modes KubePrism must be enabled on port 7445 on every
node (adopt checks this again later). --tunnel (vxlan or geneve) is immutable afterwards.
--cilium-values is a YAML file of Cilium Helm values merged like spec.cni.cilium.values;
Ichor-owned keys are refused.

It shows what it will do and asks for confirmation unless --yes. Running it again is safe.`,
	Example: `  ichor bootstrap cni --kube-proxy replaced
  ichor bootstrap cni --kube-proxy enabled --tunnel vxlan --yes`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		o := cniBootstrapOpts

		user, err := readCiliumValuesFile(o.CiliumValues)
		if err != nil {
			return fmt.Errorf("bootstrap cni: %w", err)
		}

		rc, err := loadKubeconfig(kubeconfigPath)
		if err != nil {
			return err
		}

		target, err := newKubeClientForConfig(rc)
		if err != nil {
			return fmt.Errorf("bootstrap cni: building the Kubernetes client: %w", err)
		}

		return runCNIBootstrap(cmd.Context(), cniBootstrapDeps{
			Target: target, KubeProxy: o.KubeProxy, Tunnel: o.Tunnel, Values: user,
			Stdin: cmd.InOrStdin(), Stderr: cmd.ErrOrStderr(),
			Install: func(ctx context.Context, c *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode) error {
				_, err := cilium.Bootstrap(ctx, rc, c, mode, cilium.DefaultApplyTimeout)

				return err
			},
		})
	},
}

type cniBootstrapDeps struct {
	Target    client.Reader
	KubeProxy string
	Tunnel    string
	Values    map[string]any
	Stdin     io.Reader
	Stderr    io.Writer
	// Install is cilium.Bootstrap with the operator's kubeconfig.
	Install func(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode) error
}

// bootstrapCluster is the TalosCluster-shaped input of the effective values: what adopt writes for
// a fresh cluster whose Cilium it installs (preset cilium, kube-proxy mode, tunnel, user values).
func bootstrapCluster(kubeProxy, tunnel string, user map[string]any) (*ichorv1alpha1.TalosCluster, error) {
	c := &ichorv1alpha1.TalosCluster{}
	c.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: cniCilium, KubeProxy: kubeProxy, Tunnel: tunnel}

	if len(user) > 0 {
		if err := setCiliumUserValues(c, user, kubeProxy, tunnel); err != nil {
			return nil, fmt.Errorf("--cilium-values: %w", err)
		}
	}

	return c, nil
}

func runCNIBootstrap(ctx context.Context, d cniBootstrapDeps) error {
	if d.KubeProxy != kubeProxyEnabled && d.KubeProxy != kubeProxyReplaced {
		return fmt.Errorf("bootstrap cni: --kube-proxy %q is invalid: must be %q or %q", d.KubeProxy, kubeProxyEnabled, kubeProxyReplaced)
	}

	if err := checkTunnel(d.Tunnel); err != nil {
		return fmt.Errorf("bootstrap cni: %w", err)
	}

	cluster, err := bootstrapCluster(d.KubeProxy, d.Tunnel, d.Values)
	if err != nil {
		return fmt.Errorf("bootstrap cni: %w", err)
	}

	// Validate the values the release would get before anything is read or written.
	if _, err := cilium.EffectiveValuesForMode(cluster, cni.KubeProxyMode(d.KubeProxy)); err != nil {
		return fmt.Errorf("bootstrap cni: %w", err)
	}

	facts, err := gatherK8sFacts(ctx, d.Target)
	if err != nil {
		return fmt.Errorf("bootstrap cni: inspecting the CNI: %w", err)
	}

	rep := detectCNI(facts, CNIFacts{Kind: CNINone})

	// A rerun after a failed wait: Ichor's own Cilium is already there; Apply upgrades it.
	rerun := rep.Kind == detectedCilium && rep.Provenance == provenanceIchor

	switch {
	case rerun:
	case !rep.NoCNI:
		return fmt.Errorf("bootstrap cni: this cluster is not a cluster with no CNI (found: %s); bootstrap installs Cilium only where no pod can run. "+
			"If the cluster has a CNI, run helm install ichor, then ichor adopt", rep)
	default:
		if err := checkNoCNIPreconditions(rep, nil, ""); err != nil {
			return fmt.Errorf("bootstrap cni: %w", err)
		}
	}

	_, _ = fmt.Fprintf(d.Stderr, "bootstrap cni: CNI: %s\n", rep)

	plan := fmt.Sprintf("Install Cilium %s as Ichor's Helm release (release cilium in kube-system, record in %s), "+
		"tunnel %s, kube-proxy %s, KubePrism on 7445 assumed, and wait for every node to be Ready.\n",
		cilium.ChartVersion, cilium.RecordNamespace, cmp.Or(d.Tunnel, "the default"), d.KubeProxy)

	if err := confirmPlan(d.Stdin, d.Stderr, "bootstrap cni", plan); err != nil {
		return err
	}

	if err := d.Install(ctx, cluster, cni.KubeProxyMode(d.KubeProxy)); err != nil {
		return fmt.Errorf("bootstrap cni: installing Cilium: %w; run it again to continue (the install is safe to repeat)", err)
	}

	_, _ = fmt.Fprintln(d.Stderr, "bootstrap cni: Cilium is installed and the nodes are Ready. Next: helm install ichor, then ichor adopt.")

	return nil
}

func init() {
	addAPIFlags(bootstrapCniCmd)
	bootstrapCmd.AddCommand(bootstrapCniCmd)

	f := bootstrapCniCmd.Flags()
	f.StringVar(&cniBootstrapOpts.KubeProxy, "kube-proxy", "", "enabled (Talos runs kube-proxy) or replaced (Cilium replaces it) (required)")
	f.StringVar(&cniBootstrapOpts.Tunnel, "tunnel", "", "tunnel protocol, vxlan or geneve; immutable afterwards")
	f.StringVar(&cniBootstrapOpts.CiliumValues, "cilium-values", "", "YAML file of Cilium Helm values (Ichor-owned keys are refused)")
	_ = bootstrapCniCmd.MarkFlagRequired("kube-proxy")
}
