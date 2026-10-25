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

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSpaHandler(t *testing.T) {
	files := fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
	handler := spaHandler(files)

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "existing file is served as-is", path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "console.log('app')"},
		{name: "unknown path falls back to index.html", path: "/clusters/does-not-exist", wantStatus: http.StatusOK, wantBody: "<html>index</html>"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("GET %s = status %d, want %d", tc.path, rec.Code, tc.wantStatus)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Fatalf("GET %s body = %q, want %q", tc.path, got, tc.wantBody)
			}
		})
	}
}
