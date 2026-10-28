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

package argocd_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart/loader"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/stack/argocd"
)

type obj struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
}

func objects(t *testing.T, manifest []byte) []obj {
	t.Helper()

	var out []obj

	for doc := range strings.SplitSeq(string(manifest), "\n---\n") {
		var o obj
		if err := sigsyaml.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatal(err)
		}

		if o.Kind != "" {
			out = append(out, o)
		}
	}

	return out
}

func TestEmbeddedChartIsThePinnedOne(t *testing.T) {
	ch, err := loader.LoadFile("chart/argo-cd-" + argocd.ChartVersion + ".tgz")
	if err != nil {
		t.Fatal(err)
	}

	md := ch.Metadata
	if md.Name != argocd.ChartName || md.Version != argocd.ChartVersion || md.AppVersion != argocd.AppVersion {
		t.Fatalf("chart = %s %s (app %s)", md.Name, md.Version, md.AppVersion)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a, err := argocd.Render(argocd.Options{RedisPassword: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	b, _ := argocd.Render(argocd.Options{RedisPassword: "pw"})
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Fatal("Render is not deterministic")
	}

	sum := sha256.Sum256(a)
	t.Logf("rendered %d bytes, sha256 %s", len(a), hex.EncodeToString(sum[:]))
}

func TestRenderRequiresARedisPassword(t *testing.T) {
	if _, err := argocd.Render(argocd.Options{}); err == nil {
		t.Fatal("want an error without a Redis password")
	}
}

func TestMinimalArgoCDHasNoExcludedComponents(t *testing.T) {
	m, err := argocd.Render(argocd.Options{RedisPassword: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for _, o := range objects(t, m) {
		names = append(names, o.Kind+"/"+o.Metadata.Name)

		n := o.Metadata.Name
		if strings.Contains(n, "dex") || strings.Contains(n, "notifications") || strings.Contains(n, "applicationset") {
			// The ApplicationSet CRD is not a controller; the chart installs it via crds.install.
			if o.Kind == "CustomResourceDefinition" {
				continue
			}

			t.Errorf("excluded component rendered: %s/%s", o.Kind, n)
		}

		if strings.Contains(n, "redis-ha") {
			t.Errorf("redis-ha rendered: %s/%s", o.Kind, n)
		}
	}

	for _, want := range []string{
		"Deployment/argocd-server", "Deployment/argocd-repo-server", "Deployment/argocd-redis",
		"StatefulSet/argocd-application-controller", "AppProject/default",
		"Secret/argocd-redis", "Secret/ichor-kokumi-registry", "CustomResourceDefinition/applications.argoproj.io",
	} {
		found := false

		for _, n := range names {
			found = found || n == want
		}

		if !found {
			t.Errorf("%s missing from %v", want, names)
		}
	}

	if bytes.Contains(m, []byte("helm.sh/hook")) {
		t.Error("a Helm hook leaked into the output")
	}
}

func TestUserValuesOverrideDefaults(t *testing.T) {
	m, err := argocd.Render(argocd.Options{
		RedisPassword: "pw",
		Values:        map[string]any{"notifications": map[string]any{"enabled": true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(m, []byte("argocd-notifications-controller")) {
		t.Fatal("user value notifications.enabled=true was not applied")
	}
}
