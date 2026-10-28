// Package kubetrust lets the controller's own API client trust a Kubernetes CA before the API
// server switches to it (ADR 0063, CA rotation): the verification pool is the in-cluster ca.crt
// plus extra CA certificates that can be added at runtime and survive a container restart.
package kubetrust

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// DefaultFile is where the extra CA certificates are kept; an emptyDir is mounted on its directory.
const DefaultFile = "/var/run/ichor/trust/kubernetes-ca.crt"

// TrustFile is the file Publish writes and start-up reads; tests point it elsewhere.
var TrustFile = DefaultFile

// Pool verifies a server chain against base CA plus extra certificates.
type Pool struct {
	mu       sync.RWMutex
	baseFile string
	baseData []byte
	extra    []*x509.Certificate
}

// Shared is the process-wide pool installed by Apply and extended by Publish.
var Shared = &Pool{}

// certs returns only the CERTIFICATE blocks of data; keys and anything else are dropped.
func certs(data []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate

	for {
		var b *pem.Block

		b, data = pem.Decode(data)
		if b == nil {
			break
		}

		if b.Type != "CERTIFICATE" {
			continue
		}

		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// Add trusts the certificates in pemData (other PEM blocks are ignored).
func (p *Pool) Add(pemData []byte) error {
	cs, err := certs(pemData)
	if err != nil {
		return err
	}

	if len(cs) == 0 {
		return errors.New("no certificate found")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, c := range cs {
		dup := false

		for _, e := range p.extra {
			if e.Equal(c) {
				dup = true

				break
			}
		}

		if !dup {
			p.extra = append(p.extra, c)
		}
	}

	return nil
}

// PEM returns the extra certificates, certificates only.
func (p *Pool) PEM() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]byte, 0, len(p.extra)*1024)
	for _, c := range p.extra {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}

	return out
}

// LoadFile adds the certificates in path; a missing file is not an error.
func (p *Pool) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	return p.Add(data)
}

// Publish trusts cert (public part only) and writes the extra certificates to path atomically.
func (p *Pool) Publish(cert []byte, path string) error {
	if err := p.Add(cert); err != nil {
		return fmt.Errorf("trusting the new CA: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, p.PEM(), 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// roots builds the current pool; the base file is re-read so a refreshed projected ca.crt counts.
func (p *Pool) roots() *x509.CertPool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pool := x509.NewCertPool()

	base := p.baseData
	if p.baseFile != "" {
		if b, err := os.ReadFile(p.baseFile); err == nil {
			base = b
		}
	}

	pool.AppendCertsFromPEM(base)

	for _, c := range p.extra {
		pool.AddCert(c)
	}

	return pool
}

// Verify checks the peer chain against the pool and that it is valid for host (a name or an IP;
// ConnectionState.ServerName is empty for an IP, so the caller's host is used, never the empty name).
func (p *Pool) Verify(cs tls.ConnectionState, host string) error {
	if host == "" {
		return errors.New("no server name to verify")
	}

	if len(cs.PeerCertificates) == 0 {
		return errors.New("the server sent no certificate")
	}

	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}

	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots: p.roots(), Intermediates: inter, DNSName: host,
	})

	return err
}

// Apply makes cfg verify the API server against the pool. A config that skips verification, uses
// client certificates or already has a custom transport is left alone. Verification is never skipped:
// the standard library check is replaced by Verify, which still checks the chain and host name.
func (p *Pool) Apply(cfg *rest.Config) {
	if cfg.Insecure || cfg.Transport != nil || len(cfg.CertData) > 0 || cfg.CertFile != "" || len(cfg.KeyData) > 0 || cfg.KeyFile != "" || cfg.ExecProvider != nil || cfg.AuthProvider != nil ||
		(len(cfg.CAData) == 0 && cfg.CAFile == "") {
		return
	}

	host := cfg.ServerName

	if host == "" {
		u, _, err := rest.DefaultServerURL(cfg.Host, "", schema.GroupVersion{}, true)
		if err != nil {
			return
		}

		host = u.Hostname()
	}

	p.mu.Lock()
	p.baseFile, p.baseData = cfg.CAFile, cfg.CAData
	p.mu.Unlock()

	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // stdlib guarantees it
	t.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.ServerName,
		// The default check is replaced by VerifyConnection below, which fails the handshake on any error.
		InsecureSkipVerify: true, //nolint:gosec // verified in VerifyConnection
		VerifyConnection:   func(cs tls.ConnectionState) error { return p.Verify(cs, host) },
	}
	t.ForceAttemptHTTP2 = true

	cfg.Transport = t
	cfg.TLSClientConfig = rest.TLSClientConfig{}
}
