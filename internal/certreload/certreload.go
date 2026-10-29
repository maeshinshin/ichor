// Package certreload serves a TLS certificate from files and follows changes to them
// (cert-manager renews the Secret, the kubelet swaps the mounted files) without a restart.
package certreload

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
)

// Interval is how often the files are re-read when no file event arrives.
const Interval = 10 * time.Second

// GetCertificate returns a tls.Config.GetCertificate for the pair at certPath and keyPath.
// The files need not exist yet: handshakes fail until they do. Once loaded, a certificate
// that later fails to parse is ignored and the last good one keeps being served; the watcher
// logs the error (never the key). The watcher stops when ctx ends.
func GetCertificate(ctx context.Context, certPath, keyPath string, interval time.Duration) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var (
		mu sync.Mutex
		cw *certwatcher.CertWatcher
	)

	return func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()

		if cw == nil {
			w, err := certwatcher.New(certPath, keyPath)
			if err != nil {
				return nil, errors.New("certificate not available yet: " + err.Error())
			}

			w.WithWatchInterval(interval)

			go func() { _ = w.Start(ctx) }()

			cw = w
		}

		return cw.GetCertificate(h)
	}
}
