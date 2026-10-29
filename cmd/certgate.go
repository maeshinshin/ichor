package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// certFilesExist reports whether both the certificate and the key are present in dir.
func certFilesExist(dir, certName, keyName string) bool {
	for _, n := range []string{certName, keyName} {
		if fi, err := os.Stat(filepath.Join(dir, n)); err != nil || fi.Size() == 0 {
			return false
		}
	}
	return true
}

// waitForCertFiles blocks until the certificate and key exist in dir, or ctx ends. It is how a
// replica that does not run the (leader-only) rotator learns that the leader has written the
// Secret and the kubelet has synced the volume (cold start).
func waitForCertFiles(ctx context.Context, dir, certName, keyName string, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for !certFilesExist(dir, certName, keyName) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// gatedChecker fails until ready is closed (the webhook server is only registered, and so only
// started, once its certificate exists) and then delegates to the checker get returns.
func gatedChecker(ready <-chan struct{}, get func() healthz.Checker) healthz.Checker {
	return func(req *http.Request) error {
		select {
		case <-ready:
			return get()(req)
		default:
			return errors.New("webhook serving certificate not available yet")
		}
	}
}
