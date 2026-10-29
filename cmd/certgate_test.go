package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

func TestGatedCheckerNotReadyUntilGateCloses(t *testing.T) {
	ready := make(chan struct{})
	calls := 0
	c := gatedChecker(ready, func() healthz.Checker {
		return func(*http.Request) error { calls++; return nil }
	})
	if err := c(nil); err == nil {
		t.Fatal("ready before the certificate gate closed")
	}
	if calls != 0 {
		t.Fatal("delegate consulted (and so the webhook server created) before the gate closed")
	}
	close(ready)
	if err := c(nil); err != nil {
		t.Fatalf("not ready after the gate closed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("delegate calls = %d, want 1", calls)
	}
}

func TestGatedCheckerPropagatesDelegateError(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	want := errors.New("not started")
	c := gatedChecker(ready, func() healthz.Checker { return func(*http.Request) error { return want } })
	if err := c(nil); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestWaitForCertFiles(t *testing.T) {
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { done <- waitForCertFiles(context.Background(), dir, "tls.crt", "tls.key", 5*time.Millisecond) }()

	select {
	case err := <-done:
		t.Fatalf("returned with no files: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("returned with only the certificate: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after both files appeared")
	}
}

func TestWaitForCertFilesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForCertFiles(ctx, t.TempDir(), "tls.crt", "tls.key", time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
