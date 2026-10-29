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
	"slices"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// TrustFile is where the extra CA certificates are kept (an emptyDir is mounted on its directory);
// Publish writes it and start-up reads it, tests point it elsewhere.
var TrustFile = "/var/run/ichor/trust/kubernetes-ca.crt"

// Pool verifies a server chain against base CA plus extra certificates.
type Pool struct {
	mu       sync.RWMutex
	baseFile string
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

// add trusts the certificates in pemData (other PEM blocks are ignored) and returns the ones
// that were not trusted already.
func (p *Pool) add(pemData []byte) ([]*x509.Certificate, error) {
	cs, err := certs(pemData)
	if err != nil {
		return nil, err
	}

	if len(cs) == 0 {
		return nil, errors.New("no certificate found")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var added []*x509.Certificate

	for _, c := range cs {
		if !slices.ContainsFunc(p.extra, c.Equal) {
			p.extra = append(p.extra, c)
			added = append(added, c)
		}
	}

	return added, nil
}

// LoadFile adds the certificates in path; a missing file is not an error. It logs how many it loaded.
func (p *Pool) LoadFile(path string) error {
	log := ctrllog.Log.WithName("kubetrust")

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		log.Info("Loaded extra CA certificates from the trust file", "count", 0, "path", path)

		return nil
	}

	if err != nil {
		return err
	}

	added, err := p.add(data)
	if err != nil {
		return err
	}

	log.Info("Loaded extra CA certificates from the trust file", "count", len(added), "path", path)

	return nil
}

// Publish trusts cert (public part only) and writes the extra certificates to path atomically.
func (p *Pool) Publish(cert []byte, path string) error {
	added, err := p.add(cert)
	if err != nil {
		return fmt.Errorf("trusting the new CA: %w", err)
	}

	for _, c := range added {
		ctrllog.Log.WithName("kubetrust").Info("Trusting a new CA certificate",
			"subject", c.Subject.String(), "notBefore", c.NotBefore, "notAfter", c.NotAfter)
	}

	// Nothing new and the file is there: it already holds exactly the extras.
	if len(added) == 0 {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	p.mu.RLock()

	var out []byte
	for _, c := range p.extra {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}

	p.mu.RUnlock()

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// Clear drops every extra certificate and removes the trust file at path, so only the base ca.crt is
// trusted again. It is idempotent: a missing file is not an error.
func (p *Pool) Clear(path string) error {
	p.mu.Lock()
	n := len(p.extra)
	p.extra = nil
	p.mu.Unlock()

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if n > 0 {
		ctrllog.Log.WithName("kubetrust").Info("Stopped trusting the extra CA certificates", "count", n)
	}

	return nil
}

// ClearOnceBaseTrusts calls Clear when the base ca.crt already carries the newest extra certificate
// (the latest rotation's new CA), so the pool can drop the extras without losing the API server. Until
// then the projected file lags and the extras stay. With no extras it only makes sure the file is gone.
// It reports whether it cleared.
func (p *Pool) ClearOnceBaseTrusts(path string) (bool, error) {
	p.mu.RLock()

	var newest *x509.Certificate
	if len(p.extra) > 0 {
		newest = p.extra[len(p.extra)-1]
	}

	base, _ := os.ReadFile(p.baseFile)
	p.mu.RUnlock()

	if newest != nil {
		bc, _ := certs(base)
		if !slices.ContainsFunc(bc, newest.Equal) {
			return false, nil
		}
	}

	return true, p.Clear(path)
}

// roots builds the current pool; the base file is re-read so a refreshed projected ca.crt counts.
func (p *Pool) roots() *x509.CertPool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pool := x509.NewCertPool()

	if base, err := os.ReadFile(p.baseFile); err == nil {
		pool.AppendCertsFromPEM(base)
	}

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

// Apply makes cfg verify the API server against the pool; it expects the in-cluster config (CAFile,
// bearer token) and leaves any other config alone, so `make run` against a client-cert kubeconfig keeps
// its credentials. Verification is never skipped: the standard library check is replaced by Verify,
// which still checks the chain and host name.
func (p *Pool) Apply(cfg *rest.Config) {
	if cfg.CAFile == "" || cfg.CertFile != "" || len(cfg.CertData) > 0 || cfg.ExecProvider != nil {
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
	p.baseFile = cfg.CAFile
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
