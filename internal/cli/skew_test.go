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

package cli

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// testServerV04 is the server version most skew cases are measured from.
const testServerV04 = "v0.4.0"

func serverAt(t *testing.T, installed string) client.Reader {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	b := fake.NewClientBuilder().WithScheme(scheme)
	if installed != "" {
		b = b.WithObjects(&ichorv1alpha1.IchorInstallation{
			Name:   ichorv1alpha1.IchorInstallationName,
			Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: installed},
		})
	}

	return b.Build()
}

func TestCheckServerSkew(t *testing.T) {
	for _, tc := range []struct {
		name, cli, server string
		wantErr           bool
	}{
		{name: "same version", cli: "v0.4.2", server: "v0.4.2"},
		{name: "patch apart", cli: "v0.4.9", server: testServerV04},
		{name: "one minor ahead", cli: "v0.5.0", server: testServerV04},
		{name: "one minor behind", cli: "v0.3.0", server: testServerV04},
		{name: "two minors ahead", cli: "v0.6.0", server: testServerV04, wantErr: true},
		{name: "two minors behind", cli: "v0.2.0", server: testServerV04, wantErr: true},
		{name: "a dev CLI cannot be judged", cli: "dev", server: testServerV04},
		{name: "a server that reports nothing cannot be judged", cli: "v0.6.0", server: ""},
		{name: "different majors cannot be compared", cli: "v2.0.0", server: testServerV04},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkServerSkew(context.Background(), serverAt(t, tc.server), tc.cli)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}

			if err != nil && !strings.Contains(err.Error(), "writes are refused") {
				t.Errorf("error %q does not say what is refused", err)
			}
		})
	}
}

func TestEveryWriteCommandIsGatedAndReadsAreNot(t *testing.T) {
	for _, c := range []struct {
		name  string
		gated bool
		ann   map[string]string
	}{
		{"machine approve", true, approveNodeCmd.Annotations},
		{"machine reject", true, rejectNodeCmd.Annotations},
		{"patch create", true, patchCreateCmd.Annotations},
		{"patch update", true, patchUpdateCmd.Annotations},
		{"patch delete", true, deletePatchCmd.Annotations},
		{"patch list", false, getPatchesCmd.Annotations},
		{"patch preview", false, previewCmd.Annotations},
		{"machine list", false, getNodesCmd.Annotations},
	} {
		if got := c.ann[writesAnnotation] == "true"; got != c.gated {
			t.Errorf("%s: gated = %v, want %v", c.name, got, c.gated)
		}
	}
}

// TestRootVersionMatchesVersionClient keeps `ichor --version` and `ichor version --client` on one source.
func TestRootVersionMatchesVersionClient(t *testing.T) {
	if got, want := rootCmd.Version, clientVersion(); got != want {
		t.Errorf("rootCmd.Version = %q, clientVersion() = %q", got, want)
	}
}
