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
	"io"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// uiPollInterval is how often runUI re-lists pods while waiting for a
// ready one -- long enough not to hammer the API server, short enough
// that a rollout's new pod is picked up quickly. A var, not a const, so
// tests can shrink it instead of running at wall-clock speed.
var uiPollInterval = 2 * time.Second

// uiOptions is uiCmd's flags, kept as its own type so a test can build one
// without going through cobra.
type uiOptions struct {
	Namespace   string
	LocalPort   int
	OpenBrowser bool
}

// uiDeps is runUI's dependencies. Forward and OpenBrowser are the two
// seams a test replaces: the real ones (ui_portforward.go, ui_browser.go)
// need an actual cluster and an actual browser, neither of which a unit
// test has.
type uiDeps struct {
	K8s client.Client

	// Forward opens a tunnel to pod's remotePort on localhost:localPort
	// and blocks until it ends (the context is cancelled, or the
	// connection is lost -- a deleted or evicted pod looks the same as a
	// lost connection from here). It closes ready once the tunnel is
	// actually up.
	Forward func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int32, ready chan struct{}) error

	// OpenBrowser opens url in the user's default browser.
	OpenBrowser func(url string) error

	Stdout, Stderr io.Writer
}

// podReady reports whether pod is a forwarding target: running, marked
// Ready, and not already on its way out.
func podReady(pod *corev1.Pod) bool {
	return pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning &&
		slices.ContainsFunc(pod.Status.Conditions, func(c corev1.PodCondition) bool {
			return c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue
		})
}

// selectPod is the re-forward decision itself, kept pure and separate
// from any Kubernetes call so it can be tested directly: among pods's
// ready members, prefer one other than exclude (the pod that just
// disappeared or lost its connection), falling back to exclude itself if
// it is the only ready pod there is. Picking the lowest name among ties
// keeps the result deterministic instead of depending on API list order.
func selectPod(pods []corev1.Pod, exclude string) (*corev1.Pod, bool) {
	var ready []corev1.Pod

	for i := range pods {
		if podReady(&pods[i]) {
			ready = append(ready, pods[i])
		}
	}

	if len(ready) == 0 {
		return nil, false
	}

	slices.SortFunc(ready, func(a, b corev1.Pod) int { return strings.Compare(a.Name, b.Name) })

	for i := range ready {
		if ready[i].Name != exclude {
			return &ready[i], true
		}
	}

	return &ready[0], true
}

// waitForReadyPod polls k8sClient until selectPod finds a target or ctx
// is done, printing one "waiting" line (not one per poll) so the command
// never looks hung.
func waitForReadyPod(
	ctx context.Context, k8sClient client.Client, namespace string, selector map[string]string, exclude string, stderr io.Writer,
) (*corev1.Pod, error) {
	printedWaiting := false

	for {
		var pods corev1.PodList
		if err := k8sClient.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels(selector)); err != nil {
			return nil, fmt.Errorf("ui: listing ichor-ui pods: %w", err)
		}

		if pod, ok := selectPod(pods.Items, exclude); ok {
			return pod, nil
		}

		if !printedWaiting {
			h := newHuman(stderr)
			h.Note(h.Mark(markPending) + " waiting for a ready ichor-ui pod in namespace " + namespace)

			printedWaiting = true
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(uiPollInterval):
		}
	}
}

// runUI is uiCmd's RunE, factored out so a test can drive it against a
// fake client.Client and a fake Forward instead of a real cluster and a
// real browser. It never returns an error for a clean Ctrl-C: ctx being
// cancelled is the expected way this loop ends.
func runUI(ctx context.Context, opts uiOptions, deps uiDeps) error {
	svc, err := findUIService(ctx, deps.K8s, opts.Namespace)
	if err != nil {
		return fmt.Errorf("ui: %w", err)
	}

	if len(svc.Spec.Ports) == 0 {
		return fmt.Errorf("ui: Service %s/%s has no ports", svc.Namespace, svc.Name)
	}

	if len(svc.Spec.Selector) == 0 {
		return fmt.Errorf("ui: Service %s/%s has no selector", svc.Namespace, svc.Name)
	}

	remotePort, caPEM := uiEndpoint(ctx, deps.K8s, svc, false)
	scheme := "http"

	if remotePort == uiHTTPSPort {
		scheme = "https"
	}

	localPort := int32(opts.LocalPort)
	if localPort == 0 {
		localPort = remotePort
	}

	h := newHuman(deps.Stderr)
	url := fmt.Sprintf("%s://localhost:%d/", scheme, localPort)
	opened := false
	lastPod := ""

	for {
		pod, err := waitForReadyPod(ctx, deps.K8s, opts.Namespace, svc.Spec.Selector, lastPod, deps.Stderr)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}

		reforwarded := lastPod != ""
		lastPod = pod.Name

		ready := make(chan struct{})
		done := make(chan error, 1)

		go func() { done <- deps.Forward(ctx, pod, localPort, remotePort, ready) }()

		isReady := false
		finished := false

		select {
		case <-ready:
			isReady = true
		case err = <-done:
			finished = true
			// Forward closes ready before it returns, but when both are
			// pending select picks at random: look at ready explicitly so a
			// tunnel that ends right after becoming ready still counts as
			// ready and the browser still opens.
			select {
			case <-ready:
				isReady = true
			default:
			}
		}

		if isReady && reforwarded {
			h.Note(h.Mark(markOK) + " re-established the port-forward -> pod/" + pod.Name)
		}

		if isReady && !opened {
			h.Note(h.Mark(markOK) + " ichor-ui  " + url)
			h.Line(h.Paint(sgrDim, "port-forward to "+opts.Namespace+"/"+pod.Name+" · Ctrl-C to stop"))

			if len(caPEM) > 0 {
				h.Line(h.Paint(sgrDim, fmt.Sprintf("signed by the Ichor serving CA (ConfigMap %s/%s, key %s): import it into your browser to remove the warning",
					svc.Namespace, uiCAConfigMap, uiCAKey)))
			}

			if opts.OpenBrowser {
				if err := deps.OpenBrowser(url); err != nil {
					h.Note(h.Mark(markWarning) + " could not open a browser automatically: " + err.Error())
				}
			}

			opened = true
		}

		if !finished {
			err = <-done
		}

		if ctx.Err() != nil {
			return nil
		}

		if err != nil {
			h.Note(fmt.Sprintf("%s lost the port-forward to pod/%s (%v); looking for another pod", h.Mark(markWarning), pod.Name, err))
		} else {
			h.Note(fmt.Sprintf("%s the port-forward to pod/%s ended; looking for another pod", h.Mark(markWarning), pod.Name))
		}
	}
}
