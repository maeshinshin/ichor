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
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
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
	// The identity read and this apply share one pin (runInit): a node that answers with another
	// certificate in between is refused (ADR 0108).
	c, err := client.New(ctx, controller.PinFromContext(ctx).ClientOptions(endpoint)...)
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
	// defaultWebhookWait covers a controller that takes a few minutes to become Ready on a
	// first install: its image pull, then the webhook certificate being written and mounted.
	defaultWebhookWait = 30 * time.Minute
)

// notUpYet reports the two answers of a node that is still coming up: Unavailable (it serves
// its maintenance certificate, or nothing, until it has installed and rebooted) and
// FailedPrecondition ("bootstrap is not available yet": etcd's service has not started).
func notUpYet(err error) bool {
	c := status.Code(err)

	return c == codes.Unavailable || c == codes.FailedPrecondition
}

// pollWhile runs attempt every interval (first call at once) for up to timeout, retrying
// while retryable(err) holds and printing "init: waiting for <what>: <err>" before each
// retry. A nil or non-retryable error returns at once; when the wait runs out it returns the
// last attempt's error rather than the poll's bare deadline error. Zero timeout or interval
// take the defaults.
func pollWhile(
	ctx context.Context, timeout, interval time.Duration, progress io.Writer, what string,
	attempt func(context.Context) error, retryable func(error) bool,
) error {
	if timeout <= 0 {
		timeout = defaultBootstrapWait
	}

	if interval <= 0 {
		interval = defaultBootstrapInterval
	}

	var last error

	pollErr := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		err := attempt(ctx)
		if err != nil && ctx.Err() != nil && last != nil {
			// This wait's own deadline cut the call short ("client rate limiter Wait returned an
			// error: context deadline exceeded"): report the last answer the server really gave.
			return false, nil
		}

		last = err
		if last == nil || !retryable(last) {
			return true, nil
		}

		_, _ = fmt.Fprintf(progress, "init: waiting for %s: %v\n", what, last)

		return false, nil
	})
	if pollErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	return last
}

// bootstrapWhenUp calls admin.Bootstrap, retrying while the node is not up yet for up to
// timeout. Any other error returns at once: only a node that is not there yet is worth
// waiting for.
func bootstrapWhenUp(ctx context.Context, admin bootstrapClient, timeout, interval time.Duration, progress io.Writer) error {
	return pollWhile(ctx, timeout, interval, progress, "the node to come up on its new configuration", admin.Bootstrap, notUpYet)
}

// waitForAPIServer polls until the new cluster's API server answers (any answer, even
// NotFound, means it is up) for up to timeout, so the first write is not the one that finds
// out.
func waitForAPIServer(ctx context.Context, c ctrlclient.Client, timeout, interval time.Duration, progress io.Writer) error {
	err := pollWhile(ctx, timeout, interval, progress, "the API server", func(ctx context.Context) error {
		err := c.Get(ctx, types.NamespacedName{Name: metav1.NamespaceSystem}, &corev1.Namespace{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		return err
	}, func(error) bool { return true })
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("init: the new cluster's API server did not come up: %w", err)
	}

	return err
}

// createWhenWebhooksUp creates obj, retrying while the API server cannot reach Ichor's
// admission webhooks (failurePolicy Fail; the controller serving them was started moments
// ago by the manifest init applied) for up to timeout.
func createWhenWebhooksUp(
	ctx context.Context, c ctrlclient.Client, obj ctrlclient.Object, timeout, interval time.Duration, progress io.Writer,
) error {
	tries := 0

	return pollBounded(ctx, timeout, interval, progress, "Ichor's webhooks", newWaitDiag(c, progress), func(ctx context.Context) error {
		tries++

		// An earlier attempt that timed out may still have gone through.
		if err := c.Create(ctx, obj); !apierrors.IsAlreadyExists(err) || tries == 1 {
			return err
		}

		return nil
	}, func(err error) bool {
		return (apierrors.IsInternalError(err) && strings.Contains(err.Error(), "webhook")) || initRetryable(err)
	})
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
