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

package metricsauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
)

// fakeAPI answers a TokenReview the way a cluster does for a token it does not know, and every
// SubjectAccessReview with "allowed" when allow is set.
func fakeAPI(t *testing.T, authenticated, allow bool) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/apis/authentication.k8s.io/v1/tokenreviews":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview",
				"status": map[string]any{"authenticated": authenticated, "user": map[string]any{"username": "scraper"}},
			})
		case "/apis/authorization.k8s.io/v1/subjectaccessreviews":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "authorization.k8s.io/v1", "kind": "SubjectAccessReview",
				"status": map[string]any{"allowed": allow},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func scrape(t *testing.T, srv *httptest.Server, token string) (code int, reached bool) {
	t.Helper()

	filter, err := WithAuthenticationAndAuthorization(&rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	h, err := filter(logr.Discard(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, reached
}

func TestMetricsStatusCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		authenticated, allow bool
		token                string
		want                 int
	}{
		"no token": {false, false, "", http.StatusUnauthorized},
		"a token the cluster does not accept (was 500)": {false, false, "nope", http.StatusUnauthorized},
		"a valid token without the right":               {true, false, "good", http.StatusForbidden},
		"a valid token with the right":                  {true, true, "good", http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			code, reached := scrape(t, fakeAPI(t, tc.authenticated, tc.allow), tc.token)
			if code != tc.want {
				t.Fatalf("got %d, want %d", code, tc.want)
			}

			if reached != (tc.want == http.StatusOK) {
				t.Fatalf("handler reached = %v for %d", reached, code)
			}
		})
	}
}
