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
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// maintenanceApplier is the one maintenance-mode Talos API call init
// needs: push the first configuration a node will ever receive.
// architecture.md's "Trust" section: "Approval uses Talos's insecure
// apply, which does not authenticate the node. A separate provisioning
// network is assumed; the docs say so" -- true here too, and init's own
// help text repeats it, since nothing about the API call itself can
// enforce a network topology.
type maintenanceApplier interface {
	ApplyInsecure(ctx context.Context, cfg []byte) error
}

// dialMaintenance connects to endpoint's Talos API in maintenance mode,
// the same unauthenticated connection internal/controller's
// NewMachineryMaintenanceClient dials for discovery -- it is the only
// connection a node with no configuration yet can offer.
func dialMaintenance(ctx context.Context, endpoint string) (maintenanceApplier, error) {
	c, err := client.New(ctx, client.WithMaintenanceMode(endpoint, nil))
	if err != nil {
		return nil, fmt.Errorf("dialing maintenance Talos API at %q: %w", endpoint, err)
	}

	return &maintenanceClient{c: c}, nil
}

// maintenanceClient adapts a real *client.Client, dialed in maintenance
// mode, to maintenanceApplier.
type maintenanceClient struct {
	c *client.Client
}

// ApplyInsecure applies cfg with ApplyConfigurationRequest_AUTO: this is
// the node's first configuration, so Talos decides for itself whether
// installing to disk and rebooting is needed, the same way `talosctl
// apply-config --insecure` does.
func (m *maintenanceClient) ApplyInsecure(ctx context.Context, cfg []byte) error {
	_, err := m.c.ApplyConfiguration(ctx, &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_AUTO,
	})

	return err
}

// bootstrapClient is the Talos API calls init needs once the node has
// rebooted into the configuration ApplyInsecure pushed, and trusts the
// secrets bundle's admin identity: bootstrap etcd, and fetch kubeconfig.
// Maintenance mode no longer accepts connections once a node is
// configured, which is why this is a second, differently-authenticated
// client rather than a second method on maintenanceApplier.
type bootstrapClient interface {
	Bootstrap(ctx context.Context) error
	Kubeconfig(ctx context.Context) ([]byte, error)
}

// Defaults for bootstrapWhenUp: a node that has just taken its configuration keeps serving
// its maintenance certificate (so the admin handshake fails with Unavailable) until it has
// installed and rebooted, which takes minutes on a slow disk.
const (
	defaultBootstrapWait     = 15 * time.Minute
	defaultBootstrapInterval = 5 * time.Second
)

// notUpYet reports the two answers of a node that is still coming up: Unavailable (it serves
// its maintenance certificate, or nothing, until it has installed and rebooted) and
// FailedPrecondition ("bootstrap is not available yet": etcd's service has not started).
func notUpYet(err error) bool {
	c := status.Code(err)

	return c == codes.Unavailable || c == codes.FailedPrecondition
}

// bootstrapWhenUp calls admin.Bootstrap, retrying while the node is not up yet for up to
// wait. Any other error returns at once: only a node that is not there yet is worth waiting
// for.
func bootstrapWhenUp(ctx context.Context, admin bootstrapClient, wait, interval time.Duration, progress io.Writer) error {
	if wait <= 0 {
		wait = defaultBootstrapWait
	}

	if interval <= 0 {
		interval = defaultBootstrapInterval
	}

	deadline := time.Now().Add(wait)

	for {
		err := admin.Bootstrap(ctx)
		if err == nil || !notUpYet(err) || !time.Now().Add(interval).Before(deadline) {
			return err
		}

		_, _ = fmt.Fprintf(progress, "init: waiting for the node to come up on its new configuration: %v\n", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// waitForAPIServer polls until the new cluster's API server answers (any answer, even
// NotFound, means it is up) for up to wait, so the first write is not the one that finds out.
func waitForAPIServer(ctx context.Context, c ctrlclient.Client, wait, interval time.Duration, progress io.Writer) error {
	if wait <= 0 {
		wait = defaultBootstrapWait
	}

	if interval <= 0 {
		interval = defaultBootstrapInterval
	}

	deadline := time.Now().Add(wait)

	for {
		err := c.Get(ctx, types.NamespacedName{Name: metav1.NamespaceSystem}, &corev1.Namespace{})
		if err == nil || apierrors.IsNotFound(err) {
			return nil
		}

		if !time.Now().Add(interval).Before(deadline) {
			return fmt.Errorf("init: the new cluster's API server did not come up: %w", err)
		}

		_, _ = fmt.Fprintf(progress, "init: waiting for the API server: %v\n", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// createWhenWebhooksUp creates obj, retrying while the API server cannot reach Ichor's
// admission webhooks (failurePolicy Fail; the controller serving them was started moments
// ago by the manifest init applied) for up to wait.
func createWhenWebhooksUp(
	ctx context.Context, c ctrlclient.Client, obj ctrlclient.Object, wait, interval time.Duration, progress io.Writer,
) error {
	if wait <= 0 {
		wait = defaultBootstrapWait
	}

	if interval <= 0 {
		interval = defaultBootstrapInterval
	}

	deadline := time.Now().Add(wait)

	for {
		err := c.Create(ctx, obj)
		if err == nil || !apierrors.IsInternalError(err) || !strings.Contains(err.Error(), "webhook") ||
			!time.Now().Add(interval).Before(deadline) {
			return err
		}

		_, _ = fmt.Fprintf(progress, "init: waiting for Ichor's webhooks: %v\n", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// dialAdmin connects to endpoint using the bundle's admin identity, the
// same certificate-generation recipe internal/controller's
// NewMachineryTalosClient uses for a cluster Ichor already manages. init
// has no Ichor-issued credential to read back yet -- it is creating this
// trust relationship for the first time -- so it derives the certificate
// from the bundle it just generated, directly.
func dialAdmin(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, endpoint string, bundle *secrets.Bundle) (bootstrapClient, error) {
	cert, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, fmt.Errorf("generating admin client certificate: %w", err)
	}

	cfg := clientconfig.NewConfig(cluster.Name, []string{endpoint}, bundle.Certs.OS.Crt, cert)

	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dialing Talos API at %q: %w", endpoint, err)
	}

	return &adminClient{c: c}, nil
}

// adminClient adapts a real *client.Client, dialed with the bundle's
// admin identity, to bootstrapClient.
type adminClient struct {
	c *client.Client
}

// Bootstrap bootstraps etcd on this node (design step 4: "Bootstrap
// etcd").
func (a *adminClient) Bootstrap(ctx context.Context) error {
	return a.c.Bootstrap(ctx, &machineapi.BootstrapRequest{})
}

// Kubeconfig fetches the admin kubeconfig (design step 4: "fetch the
// kubeconfig").
func (a *adminClient) Kubeconfig(ctx context.Context) ([]byte, error) {
	return a.c.Kubeconfig(ctx)
}
