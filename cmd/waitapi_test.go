package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/maeshinshin/ichor/internal/kubetrust"
)

type fakeGroups struct{ err error }

func (f fakeGroups) ServerGroups() (*metav1.APIGroupList, error) {
	return &metav1.APIGroupList{}, f.err
}

func TestWaitForAPIServerRetriesThenSucceeds(t *testing.T) {
	configs, probes := 0, 0
	cfg, err := waitForAPIServer(context.Background(),
		func() (*rest.Config, error) { configs++; return &rest.Config{Host: "h"}, nil },
		func(*rest.Config) (discovery.ServerGroupsInterface, error) {
			probes++
			if probes < 3 {
				return fakeGroups{err: errors.New("x509: certificate signed by unknown authority")}, nil
			}
			return fakeGroups{}, nil
		},
		time.Second, time.Millisecond, 2*time.Millisecond)
	if err != nil || cfg == nil || cfg.Host != "h" {
		t.Fatalf("got %v, %v", cfg, err)
	}
	if configs != 3 {
		t.Fatalf("config re-read %d times, want 3", configs)
	}
}

func failingWait(ctx context.Context, timeout time.Duration) error {
	_, err := waitForAPIServer(ctx,
		func() (*rest.Config, error) { return &rest.Config{}, nil },
		func(*rest.Config) (discovery.ServerGroupsInterface, error) {
			return fakeGroups{err: errors.New("unknown")}, nil
		},
		timeout, time.Millisecond, 2*time.Millisecond)
	return err
}

func TestWaitForAPIServerErrorSaysWhatHappened(t *testing.T) {
	err := failingWait(context.Background(), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not reachable within 20ms") {
		t.Fatalf("timeout: got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	err = failingWait(ctx, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "stopped while waiting for the API server (") ||
		strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("cancel: got %v", err)
	}
}

func TestWaitWithHealthServesDuringWaitAndClosesAfter(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	_, err = waitWithHealth(context.Background(), addr, func(context.Context) (*rest.Config, error) {
		for path, want := range map[string]int{"/healthz": 200, "/readyz": 503} {
			resp, gerr := http.Get("http://" + addr + path)
			if gerr != nil {
				t.Fatalf("%s during the wait: %v", path, gerr)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("%s: status %d, want %d (%s)", path, resp.StatusCode, want, body)
			}
		}
		return &rest.Config{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port not freed before return: %v", err)
	}
	_ = ln.Close()
}

func TestTrustedConfigLoadsTheRotationFileBeforeTheWait(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	// An unrelated self-signed CA stands for the stale in-cluster ca.crt.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "old"},
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	stale := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	pemOf := func(s *httptest.Server) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	}
	staleFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(staleFile, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	get := func() (*rest.Config, error) {
		cfg := &rest.Config{Host: srv.URL}
		cfg.CAFile = staleFile

		return cfg, nil
	}
	probe := func(cfg *rest.Config) error {
		rt, err := rest.TransportFor(cfg)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Transport: rt}).Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	missing := filepath.Join(t.TempDir(), "none.crt")
	cfg, err := trustedConfig(get, &kubetrust.Pool{}, missing)
	if err != nil {
		t.Fatal(err)
	}
	if probe(cfg) == nil {
		t.Fatal("trusted a server signed by neither the in-cluster CA nor the file")
	}

	file := filepath.Join(t.TempDir(), "kubernetes-ca.crt")
	if err := os.WriteFile(file, pemOf(srv), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = trustedConfig(get, &kubetrust.Pool{}, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe(cfg); err != nil {
		t.Fatalf("the CA in the trust file is not trusted at start: %v", err)
	}
}
