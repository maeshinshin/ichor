package releasecheck

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const (
	v010 = "v0.1.0"
	v020 = "v0.2.0"
)

func TestLatestPicksTheHighestFinalRelease(t *testing.T) {
	tags := []string{"0.2.0", "0.10.0", "0.9.1", "0.10.0-rc.1", "1.0.0-beta.2", "latest", "1.2", "v0.11.0", "0.11.0+build.7", "0.1.0"}

	if got, want := Latest(tags), "v0.11.0"; got != want {
		t.Fatalf("Latest = %q, want %q", got, want)
	}
}

func TestFinalsAreNewestFirstAndOncePerVersion(t *testing.T) {
	got := Finals([]string{"0.4.0", "v0.10.0", "0.4.0", "0.3.0-rc.1", "v0.9.1", "junk", "v0.4.0+b.1"})

	if want := []string{"v0.10.0", "v0.9.1", "v0.4.0"}; !slices.Equal(got, want) {
		t.Fatalf("Finals = %v, want %v", got, want)
	}
}

func TestLatestWithoutReleases(t *testing.T) {
	for _, tags := range [][]string{nil, {}, {"0.1.0-rc.1", "dev"}} {
		if got := Latest(tags); got != "" {
			t.Errorf("Latest(%v) = %q, want empty", tags, got)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		name            string
		running, latest string
		want            bool
	}{
		{"older release", v010, v020, true},
		{"same release", v020, v020, false},
		{"running is newer", "v0.3.0", v020, false},
		{"patch", v020, "v0.2.1", true},
		{"demo build below a release", "v0.0.8-gabc1234", v010, true},
		{"demo build of a release", "v0.1.0-g1f2e3d4", v010, true},
		{"build metadata is ignored", "v0.2.0+meta", v020, false},
		{"dev build never prompts", "dev", "v9.0.0", false},
		{"empty running never prompts", "", "v9.0.0", false},
		{"no release known", v010, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Newer(tc.running, tc.latest); got != tc.want {
				t.Fatalf("Newer(%q, %q) = %v, want %v", tc.running, tc.latest, got, tc.want)
			}
		})
	}
}

func TestHost(t *testing.T) {
	cases := map[string]string{
		DefaultRepository:              "ghcr.io",
		"oci://ghcr.io/x/charts/ichor": "ghcr.io",
		"127.0.0.1:5000/ichor":         "127.0.0.1",
		"registry.local:443/a/b":       "registry.local",
	}

	for in, want := range cases {
		got, err := Host(in)
		if err != nil || got != want {
			t.Errorf("Host(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	if _, err := Host("nohost"); err == nil {
		t.Error("Host of a reference with no registry must fail")
	}
}

// fakeRegistry serves the two endpoints Helm's client calls for a tag listing, anonymously.
func fakeRegistry(t *testing.T, repo string, tags string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/", "/v2":
			w.WriteHeader(http.StatusOK)
		case "/v2/" + repo + "/tags/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tags))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestRegistryListerReadsTagsFromARegistry(t *testing.T) {
	const repo = "test/charts/ichor"

	srv := fakeRegistry(t, repo, `{"name":"test/charts/ichor","tags":["0.1.0","0.3.0-rc.1","0.2.0","notes"]}`)

	host := strings.TrimPrefix(srv.URL, "http://")

	list := RegistryLister(true)

	tags, err := list(host + "/" + repo)
	if err != nil {
		t.Fatalf("listing tags: %v", err)
	}

	if got, want := Latest(tags), v020; got != want {
		t.Fatalf("Latest over the listed tags = %q, want %q (tags %v)", got, want, tags)
	}
}

func TestRegistryListerFailsWhenTheRegistryDoesNotAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	list := RegistryLister(true)

	if _, err := list(strings.TrimPrefix(srv.URL, "http://") + "/test/charts/ichor"); err == nil {
		t.Fatal("a 503 from the registry must be an error")
	}
}

// The Talos, Kubernetes and Cilium repositories tag their releases with a leading v ("v1.14.3"); the chart's
// tags have none. The lister must return both kinds, or every image-based upgrade list is empty (ADR 0115).
func TestListerKeepsVPrefixedTags(t *testing.T) {
	const repo = "siderolabs/kubelet"

	srv := fakeRegistry(t, repo, `{"name":"siderolabs/kubelet","tags":["v1.35.1","v1.36.0-rc.1","v1.35.0","latest","v1.36.2-1-gabc1234"]}`)

	list := RegistryLister(true)

	tags, err := list(strings.TrimPrefix(srv.URL, "http://") + "/" + repo)
	if err != nil {
		t.Fatalf("listing tags: %v", err)
	}

	if got, want := Finals(tags), []string{"v1.35.1", "v1.35.0"}; !slices.Equal(got, want) {
		t.Fatalf("Finals over the listed tags = %v, want %v (tags %v)", got, want, tags)
	}
}

func TestListerFollowsPagedTagLists(t *testing.T) {
	const repo = "siderolabs/installer-base"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v2/"+repo+"/tags/list" && r.URL.Query().Get("last") == "":
			w.Header().Set("Link", `</v2/`+repo+`/tags/list?last=v1.13.0&n=2>; rel="next"`)
			_, _ = w.Write([]byte(`{"name":"` + repo + `","tags":["v1.12.0","v1.13.0"]}`))
		case r.URL.Path == "/v2/"+repo+"/tags/list":
			_, _ = w.Write([]byte(`{"name":"` + repo + `","tags":["v1.14.3"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	list := RegistryLister(true)

	tags, err := list(strings.TrimPrefix(srv.URL, "http://") + "/" + repo)
	if err != nil {
		t.Fatalf("listing tags: %v", err)
	}

	if got, want := Finals(tags), []string{"v1.14.3", "v1.13.0", "v1.12.0"}; !slices.Equal(got, want) {
		t.Fatalf("Finals over the listed tags = %v, want %v", got, want)
	}
}
