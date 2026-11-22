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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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

	c := &ImageFactoryClient{URL: srv.URL}
	ext := []string{"siderolabs/iscsi-tools", "siderolabs/util-linux-tools"}

	for range 2 {
		id, err := c.Register(t.Context(), ext)
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

	if _, err := (&ImageFactoryClient{URL: srv.URL}).Register(t.Context(), []string{"siderolabs/nope"}); err == nil ||
		!strings.Contains(err.Error(), "no such extension") {
		t.Fatalf("err = %v, want the factory's answer", err)
	}
}
