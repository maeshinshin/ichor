package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
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

func TestWaitForAPIServerTimesOut(t *testing.T) {
	_, err := waitForAPIServer(context.Background(),
		func() (*rest.Config, error) { return &rest.Config{}, nil },
		func(*rest.Config) (discovery.ServerGroupsInterface, error) {
			return fakeGroups{err: errors.New("unknown")}, nil
		},
		20*time.Millisecond, time.Millisecond, 2*time.Millisecond)
	if err == nil {
		t.Fatal("want timeout error")
	}
}
