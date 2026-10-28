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
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// portForwardToPod builds uiDeps.Forward's real implementation: the same
// SPDY upgrade + tools/portforward.PortForwarder kubectl's own
// "port-forward" subcommand uses, with the stream output discarded (what
// ichor ui itself prints -- ui_run.go's own status lines -- is enough;
// relaying every byte the port-forward machinery logs would just be
// noise on top of it).
func portForwardToPod(cfg *rest.Config, clientset kubernetes.Interface) func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int32, ready chan struct{}) error {
	return func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int32, ready chan struct{}) error {
		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Namespace(pod.Namespace).
			Name(pod.Name).
			SubResource("portforward")

		transport, upgrader, err := spdy.RoundTripperFor(cfg)
		if err != nil {
			return fmt.Errorf("ui: building the SPDY round tripper: %w", err)
		}

		dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())

		fw, err := portforward.NewOnAddresses(
			dialer, []string{"localhost"}, []string{fmt.Sprintf("%d:%d", localPort, remotePort)},
			ctx.Done(), ready, io.Discard, io.Discard,
		)
		if err != nil {
			return fmt.Errorf("ui: setting up the port-forward: %w", err)
		}

		return fw.ForwardPorts()
	}
}
