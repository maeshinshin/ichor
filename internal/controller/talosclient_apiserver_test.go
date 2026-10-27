/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeReadyz(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)

			return
		}

		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	// httptest's certificate is issued for example.com and 127.0.0.1.
	if err := probeReadyz(context.Background(), srv.URL+"/readyz", ca, "example.com"); err != nil {
		t.Fatalf("a ready API server: %v", err)
	}

	status = http.StatusServiceUnavailable
	if err := probeReadyz(context.Background(), srv.URL+"/readyz", ca, "example.com"); err == nil {
		t.Fatal("a 503 must not count as ready")
	}

	status = http.StatusOK
	if err := probeReadyz(context.Background(), srv.URL+"/readyz", ca, "other.example"); err == nil {
		t.Fatal("a certificate for another name must not count as ready")
	}

	if err := probeReadyz(context.Background(), srv.URL+"/readyz", []byte("not pem"), "example.com"); err == nil {
		t.Fatal("a CA that is not PEM must fail closed")
	}
}

func TestContainerID(t *testing.T) {
	status := map[string]any{"containerStatuses": []any{
		map[string]any{"name": "sidecar", "containerID": "containerd://other"},
		map[string]any{"name": apiServerContainer, "containerID": "containerd://abc"},
	}}

	if got := containerID(status, apiServerContainer); got != "containerd://abc" {
		t.Fatalf("containerID = %q", got)
	}

	if got := containerID(map[string]any{}, apiServerContainer); got != "" {
		t.Fatalf("containerID of an empty status = %q, want empty", got)
	}
}
