package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/maeshinshin/ichor/internal/kubetrust"
)

// API server wait at start: during a CA rotation the API server restarts and the pod's
// ca.crt is refreshed late, so the first discovery fails (x509 / "unknown"). Retrying here
// beats exiting and sitting in CrashLoopBackOff. Once running, client-go reloads a CA read
// from CAFile by itself (ClientsAllowCARotation, Beta and on by default in v0.36), so no
// post-start watchdog is needed. A CA rotation also leaves the new Kubernetes CA's certificate in
// kubetrust.TrustFile (an emptyDir), which trustedConfig loads before the first probe.
const (
	apiWaitTimeout    = 5 * time.Minute
	apiWaitInitialGap = time.Second
	apiWaitMaxGap     = 30 * time.Second
	// apiWaitLogEvery limits the failed-probe log to one line per this long.
	apiWaitLogEvery = 15 * time.Second
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
	start := time.Now()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	gap := initial

	var lastLog time.Time

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
		if lastLog.IsZero() || time.Since(lastLog) >= apiWaitLogEvery {
			log.Info("Waiting for the API server", "reason", err.Error(), "retryIn", gap.String(),
				"waited", time.Since(start).Round(time.Second).String())
			lastLog = time.Now()
		}

		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				return nil, fmt.Errorf("stopped while waiting for the API server (%s): %w",
					time.Since(start).Round(time.Second), err)
			}
			return nil, fmt.Errorf("API server not reachable within %s: %w", timeout, err)
		case <-time.After(gap):
		}
		gap = min(gap*2, maxGap)
	}
}

// serveWaitHealth answers /healthz (200) and /readyz (503) on addr until the returned stop is
// called, which also frees the port. The manager's own probe server binds the same address
// later, and without this the startup/liveness probes would kill the pod during the wait.
func serveWaitHealth(addr string) (func(), error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "waiting for the API server", http.StatusServiceUnavailable)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}, nil
}

// waitWithHealth runs wait while serving the temporary health endpoint on probeAddr, and
// closes it before returning. A bind failure is logged, not fatal: the wait still runs.
func waitWithHealth(
	ctx context.Context, probeAddr string, wait func(context.Context) (*rest.Config, error),
) (*rest.Config, error) {
	if stop, err := serveWaitHealth(probeAddr); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "Temporary health endpoint not started")
	} else {
		defer stop()
	}
	return wait(ctx)
}

// trustedConfig loads the CA certificates a CA rotation left in trustFile into pool (so a restarted
// container trusts the new Kubernetes CA at once) and makes the config verify against it.
func trustedConfig(get func() (*rest.Config, error), pool *kubetrust.Pool, trustFile string) (*rest.Config, error) {
	cfg, err := get()
	if err != nil {
		return nil, err
	}

	if err := pool.LoadFile(trustFile); err != nil {
		return nil, fmt.Errorf("reading %s: %w", trustFile, err)
	}

	pool.Apply(cfg)

	return cfg, nil
}

// waitForAPIServerDefault probes the in-cluster/kubeconfig API server with the production bounds.
func waitForAPIServerDefault(ctx context.Context, probeAddr string) (*rest.Config, error) {
	return waitWithHealth(ctx, probeAddr, func(ctx context.Context) (*rest.Config, error) {
		get := func() (*rest.Config, error) {
			return trustedConfig(ctrl.GetConfig, kubetrust.Shared, kubetrust.TrustFile)
		}
		// The Service address may be down for reasons only this controller can repair (apifallback.go).
		get = withAPIFallback(get, func() []string { return readAPIServers(apiServersFile(kubetrust.TrustFile)) }, apiFallbackAfter, time.Now)

		return waitForAPIServer(ctx, get,
			func(c *rest.Config) (discovery.ServerGroupsInterface, error) {
				return discovery.NewDiscoveryClientForConfig(c)
			},
			apiWaitTimeout, apiWaitInitialGap, apiWaitMaxGap)
	})
}
