package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

// API server wait at start: during a CA rotation the API server restarts and the pod's
// ca.crt is refreshed late, so the first discovery fails (x509 / "unknown"). Retrying here
// beats exiting and sitting in CrashLoopBackOff. Once running, client-go reloads a CA read
// from CAFile by itself (ClientsAllowCARotation, Beta and on by default in v0.36), so no
// post-start watchdog is needed.
const (
	apiWaitTimeout    = 5 * time.Minute
	apiWaitInitialGap = time.Second
	apiWaitMaxGap     = 30 * time.Second
)

// waitForAPIServer returns a rest config once discovery succeeds. getConfig is called on every
// attempt so a refreshed CA file is re-read; newDiscovery builds the probe client.
func waitForAPIServer(
	ctx context.Context,
	getConfig func() (*rest.Config, error),
	newDiscovery func(*rest.Config) (discovery.ServerGroupsInterface, error),
	timeout, initial, maxGap time.Duration,
) (*rest.Config, error) {
	log := logr.FromContextOrDiscard(ctx)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	gap := initial
	for {
		cfg, err := getConfig()
		if err == nil {
			var dc discovery.ServerGroupsInterface
			if dc, err = newDiscovery(cfg); err == nil {
				_, err = dc.ServerGroups()
			}
		}
		if err == nil {
			return cfg, nil
		}
		log.Info("Waiting for the API server", "reason", err.Error(), "retryIn", gap.String())
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("API server not reachable within %s: %w", timeout, err)
		case <-time.After(gap):
		}
		gap = min(gap*2, maxGap)
	}
}

// waitForAPIServerDefault probes the in-cluster/kubeconfig API server with the production bounds.
func waitForAPIServerDefault(ctx context.Context) (*rest.Config, error) {
	return waitForAPIServer(ctx, ctrl.GetConfig,
		func(c *rest.Config) (discovery.ServerGroupsInterface, error) {
			return discovery.NewDiscoveryClientForConfig(c)
		},
		apiWaitTimeout, apiWaitInitialGap, apiWaitMaxGap)
}
