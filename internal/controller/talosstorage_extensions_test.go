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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// TestImageFactoryClientPostsTheSchematicOnceAndRemembersTheID pins the request Talos v1.14 "Boot Assets" shows
// (POST /schematics with the schematic YAML) and that a repeated registration costs no second request.
func TestImageFactoryClientPostsTheSchematicOnceAndRemembersTheID(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/schematics" ||
			!strings.Contains(string(body), "officialExtensions:\n      - siderolabs/iscsi-tools\n      - siderolabs/util-linux-tools\n") {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path+" "+string(body), http.StatusBadRequest)

			return
		}

		_, _ = w.Write([]byte(`{"id":"abc123"}`))
	}))
	defer srv.Close()

	c := &ImageFactoryClient{}
	ext := []string{iscsiToolsExt, "siderolabs/util-linux-tools"}

	for range 2 {
		id, err := c.Register(t.Context(), srv.URL, ext)
		if err != nil || id != "abc123" {
			t.Fatalf("Register = %q, %v", id, err)
		}
	}

	if calls.Load() != 1 {
		t.Errorf("the factory was asked %d times, want 1", calls.Load())
	}
}

func TestImageFactoryClientReportsARefusal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such extension", http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := (&ImageFactoryClient{}).Register(t.Context(), srv.URL, []string{"siderolabs/nope"}); err == nil ||
		!strings.Contains(err.Error(), "no such extension") {
		t.Fatalf("err = %v, want the factory's answer", err)
	}
}

// TestImageFactoryClientAsksTheFactoryItIsGiven: the schematic is registered at the URL passed in
// (TalosCluster.spec.imageFactoryURL), and an ID remembered for one factory is not served for another.
func TestImageFactoryClientAsksTheFactoryItIsGiven(t *testing.T) {
	t.Parallel()

	var hits [2]atomic.Int32

	servers := [2]*httptest.Server{}
	for i := range servers {
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits[i].Add(1)
			_, _ = fmt.Fprintf(w, `{"id":"from-%d"}`, i)
		}))
		defer servers[i].Close()
	}

	c := &ImageFactoryClient{}

	for i, srv := range servers {
		id, err := c.Register(t.Context(), srv.URL, []string{iscsiToolsExt})
		if err != nil || id != fmt.Sprintf("from-%d", i) {
			t.Fatalf("factory %d: Register = %q, %v", i, id, err)
		}

		if hits[i].Load() != 1 {
			t.Errorf("factory %d was asked %d times, want 1", i, hits[i].Load())
		}
	}
}

// TestRealImageFactoryRegistersTheLonghornExtensions posts to the real Image Factory. It is skipped unless
// ICHOR_REAL_FACTORY=1, so CI never calls the network.
func TestRealImageFactoryRegistersTheLonghornExtensions(t *testing.T) {
	if os.Getenv("ICHOR_REAL_FACTORY") != "1" {
		t.Skip("set ICHOR_REAL_FACTORY=1 to post to https://factory.talos.dev")
	}

	preset, err := storage.NewPreset(storage.LonghornV1)
	if err != nil {
		t.Fatal(err)
	}

	id, err := (&ImageFactoryClient{}).Register(t.Context(), ichorv1alpha1.DefaultImageFactoryURL,
		slices.Sorted(slices.Values(preset.RequiredExtensions())))
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("schematic ID: %s", id)

	if len(id) != 64 {
		t.Errorf("schematic ID %q is not 64 hex characters", id)
	}
}
