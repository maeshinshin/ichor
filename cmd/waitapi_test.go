package main

import (
	"context"
	"errors"
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
