package main

import (
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

const (
	viaService = "https://10.0.0.1:443"
	cpOne      = "10.1.0.1"
	cpTwo      = "10.1.0.2"
)

func TestAPIFallbackTriesTheControlPlanesAfterTheDelay(t *testing.T) {
	clock := time.Unix(0, 0)
	get := withAPIFallback(
		func() (*rest.Config, error) { return &rest.Config{Host: viaService}, nil },
		func() []string { return []string{cpOne, cpTwo} },
		30*time.Second, func() time.Time { return clock })

	host := func() string {
		cfg, err := get()
		if err != nil {
			t.Fatal(err)
		}

		return cfg.Host
	}

	for range 3 {
		if h := host(); h != viaService {
			t.Fatalf("before the delay: %s", h)
		}
	}

	clock = clock.Add(time.Minute)

	// The Service address and a control plane, alternating.
	want := []string{"https://10.1.0.1:6443", viaService, "https://10.1.0.2:6443", viaService, "https://10.1.0.1:6443"}
	for i, w := range want {
		if h := host(); h != w {
			t.Fatalf("attempt %d after the delay: got %s want %s", i, h, w)
		}
	}
}

func TestAPIFallbackWithoutAddressesKeepsTheService(t *testing.T) {
	clock := time.Unix(0, 0)
	get := withAPIFallback(
		func() (*rest.Config, error) { return &rest.Config{Host: viaService}, nil },
		func() []string { return nil }, time.Second, func() time.Time { return clock })

	for range 4 {
		clock = clock.Add(time.Hour)

		if cfg, _ := get(); cfg.Host != viaService {
			t.Fatalf("got %s", cfg.Host)
		}
	}
}

func TestAPIFallbackVerifiesTheCertificateByName(t *testing.T) {
	clock := time.Unix(0, 0)
	get := withAPIFallback(
		func() (*rest.Config, error) { return &rest.Config{Host: viaService}, nil },
		func() []string { return []string{cpOne} }, 0, func() time.Time { return clock })

	cfg, _ := get()

	if cfg.ServerName != apiServerName || cfg.Insecure {
		t.Fatalf("a direct dial must verify against %s: %+v", apiServerName, cfg.TLSClientConfig)
	}
}

func TestAPIServersFileRoundTrip(t *testing.T) {
	path := apiServersFile(filepath.Join(t.TempDir(), "kubernetes-ca.crt"))
	if got := readAPIServers(path); got != nil {
		t.Fatalf("missing file: %v", got)
	}

	if err := writeAPIServers(path, []string{cpOne, cpTwo}); err != nil {
		t.Fatal(err)
	}

	if got := readAPIServers(path); len(got) != 2 || got[1] != cpTwo {
		t.Fatalf("got %v", got)
	}
}
