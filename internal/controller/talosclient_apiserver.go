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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"

	talosconfig "github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	k8sres "github.com/siderolabs/talos/pkg/machinery/resources/k8s"
)

const (
	// apiServerCertName is a name every Talos kube-apiserver serving certificate carries, so the
	// probe verifies against the cluster's Kubernetes CA whatever address it dials.
	apiServerCertName = "kubernetes"

	apiServerContainer = "kube-apiserver"
)

// APIServerInstance reads the kube-apiserver static pod's status from Talos (the kubelet's view of
// the mirror pod) and returns the container ID of its kube-apiserver container.
func (t *machineryTalosClient) APIServerInstance(ctx context.Context) (string, error) {
	list, err := safe.StateListAll[*k8sres.StaticPodStatus](ctx, t.c.COSI)
	if err != nil {
		return "", fmt.Errorf("listing static pod statuses: %w", err)
	}

	for status := range list.All() {
		if !strings.Contains(status.Metadata().ID(), apiServerContainer) {
			continue
		}

		return containerID(status.TypedSpec().PodStatus, apiServerContainer), nil
	}

	return "", nil
}

// containerID picks the containerID of the named container out of a pod status's containerStatuses.
func containerID(podStatus map[string]any, name string) string {
	statuses, _ := podStatus["containerStatuses"].([]any)
	for _, s := range statuses {
		cs, _ := s.(map[string]any)
		if cs["name"] != name {
			continue
		}

		id, _ := cs["containerID"].(string)

		return id
	}

	return ""
}

// APIServerReady GETs https://<node>:<port>/readyz, the port being the one the node's active
// configuration gives kube-apiserver. /readyz is readable anonymously (the system:public-info-viewer
// role), so no credentials are sent.
func (t *machineryTalosClient) APIServerReady(ctx context.Context) error {
	md := resource.NewMetadata(configres.NamespaceName, configres.MachineConfigType, configres.ActiveID, resource.VersionUndefined)

	res, err := safe.StateGet[*configres.MachineConfig](ctx, t.c.COSI, md)
	if err != nil {
		return fmt.Errorf("reading current machine configuration: %w", err)
	}

	return probeAPIServer(ctx, t.endpoint, res.Provider(), t.kubernetesCA, apiServerCertName)
}

// probeAPIServer probes /readyz on endpoint's host at the port cfg gives kube-apiserver:
// cluster.apiServer.port (KubeAPIServerConfig) or cluster.controlPlane.localAPIServerPort, else
// Talos's default 6443 (constants.DefaultControlPlanePort, which APIPort returns when unset).
func probeAPIServer(ctx context.Context, endpoint string, cfg talosconfig.Config, caPEM []byte, serverName string) error {
	port := strconv.Itoa(constants.DefaultControlPlanePort)
	if api := cfg.K8sAPIServerConfig(); api != nil {
		port = strconv.Itoa(api.APIPort())
	}

	host := endpoint
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	return probeReadyz(ctx, "https://"+net.JoinHostPort(host, port)+"/readyz", caPEM, serverName)
}

// probeReadyz returns nil only when url answers 200, verifying its certificate against caPEM as
// serverName.
func probeReadyz(ctx context.Context, url string, caPEM []byte, serverName string) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("the cluster's Kubernetes CA is not a PEM certificate")
	}

	httpClient := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS12}},
	}
	defer httpClient.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kube-apiserver /readyz: %w", err)
	}

	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kube-apiserver /readyz answered %s", resp.Status)
	}

	return nil
}
