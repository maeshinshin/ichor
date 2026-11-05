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
	"fmt"
	"os/signal"
	"slices"
	"syscall"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/oidctrust"
)

// uiComponentLabel/Value select the ichor-ui Service on every install path:
// config/manager-ui/service.yaml (whose name kustomize prefixes, so it is
// ichor-ichor-ui-service) and dist/chart/templates/ui/ui-service.yaml both set
// it, so the CLI never depends on a name.
const (
	uiComponentLabel = oidctrust.UIComponentLabel
	uiComponentValue = oidctrust.UIComponentValue
)

// findUIService returns the ichor-ui Service in ns, found by label.
func findUIService(ctx context.Context, c client.Client, ns string) (*corev1.Service, error) {
	var list corev1.ServiceList
	if err := c.List(ctx, &list, client.InNamespace(ns), client.MatchingLabels{uiComponentLabel: uiComponentValue}); err != nil {
		return nil, fmt.Errorf("listing ichor-ui Services in %s: %w", ns, err)
	}

	if len(list.Items) == 0 {
		return nil, fmt.Errorf("no Service labelled %s=%s in namespace %s", uiComponentLabel, uiComponentValue, ns)
	}

	return &list.Items[0], nil
}

// uiHTTPSPort and uiCAConfigMap: ichor-ui serves HTTPS on 8443
// and the controller publishes the CA that signs its leaf as this ConfigMap.
const (
	uiHTTPSPort   = 8443
	uiCAConfigMap = "ichor-ui-ca"
	uiCAKey       = "ca.crt"
)

// uiEndpoint picks the Service port to forward to and the CA PEM that verifies
// it. A Service with port 8443 and a published ichor-ui-ca (or a --ca-file the
// caller holds) is used over HTTPS; an older cluster has
// neither, and keeps the first port (the old 8080 path).
func uiEndpoint(ctx context.Context, c client.Client, svc *corev1.Service, haveCAFile bool) (int32, []byte) {
	if !slices.ContainsFunc(svc.Spec.Ports, func(p corev1.ServicePort) bool { return p.Port == uiHTTPSPort }) {
		return svc.Spec.Ports[0].Port, nil
	}

	var cm corev1.ConfigMap
	if err := c.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: uiCAConfigMap}, &cm); err == nil && cm.Data[uiCAKey] != "" {
		return uiHTTPSPort, []byte(cm.Data[uiCAKey])
	}

	// No CA to read: 8443 is used only if the caller supplied a CA or it is
	// the only port; otherwise stay on the other (plaintext) port.
	if !haveCAFile {
		for _, p := range svc.Spec.Ports {
			if p.Port != uiHTTPSPort {
				return p.Port, nil
			}
		}
	}

	return uiHTTPSPort, nil
}

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
through the ichor-ui Service (found by its component label), never a hardcoded pod name) and opens
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
			OpenBrowser: browser.OpenURL,
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
