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
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

// uiServiceName is dist/chart/templates/ui/ui-service.yaml's fixed
// (never release-templated) Service name: a port-forward target needs a
// predictable name the same way the Deployment's own comment explains.
const uiServiceName = "ichor-ui-service"

var uiOpts uiOptions

// uiCmd implements `ichor ui` (docs/design/architecture.md, "CLI and
// UI": "In the first release the UI is reached through `ichor ui` (a
// port-forward)"). It never talks to the UI's own API -- that is
// internal/uiapi's job, reached only once the tunnel exists -- and never
// mutates anything in the cluster; it only reads the ichor-ui Service and
// Pods to find something to forward to.
var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Open the Ichor UI through a port-forward to ichor-ui",
	Long: `ui opens a port-forward to one of the ichor-ui Deployment's pods (found
through the ichor-ui-service Service, never a hardcoded pod name) and opens
it in your default browser.

If the pod currently forwarded to disappears -- a rollout, a drain, an
eviction -- ui re-establishes the tunnel to another ready pod on the same
local port automatically, printing what it is doing rather than hanging
silently. Press Ctrl-C to stop.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadKubeconfig(kubeconfigPath)
		if err != nil {
			return err
		}

		k8sClient, err := newKubeClientForConfig(cfg)
		if err != nil {
			return err
		}

		clientset, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return err
		}

		deps := uiDeps{
			K8s:         k8sClient,
			Forward:     portForwardToPod(cfg, clientset),
			OpenBrowser: openBrowser,
			Stdout:      cmd.OutOrStdout(),
			Stderr:      cmd.ErrOrStderr(),
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		return runUI(ctx, uiOpts, deps)
	},
}

func init() {
	rootCmd.AddCommand(uiCmd)

	flags := uiCmd.Flags()
	flags.StringVarP(&uiOpts.Namespace, "namespace", "n", ichorControllerNamespace, "namespace ichor-ui runs in")
	flags.IntVar(&uiOpts.LocalPort, "local-port", 0, "local port to forward to (defaults to the Service's own port)")
	flags.BoolVar(&uiOpts.OpenBrowser, "open-browser", true, "open the default web browser once the port-forward is ready")
}
