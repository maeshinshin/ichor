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
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const testCLIVersion = "v0.4.0"

// failReader records every read of the cluster.
type failReader struct {
	client.Reader
	reads *int
}

func (f failReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	*f.reads++

	return errors.New("connection refused")
}

func installationWith(t *testing.T, spec, status string) client.Reader {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ichorv1alpha1.IchorInstallation{
		Name:   ichorv1alpha1.IchorInstallationName,
		Spec:   ichorv1alpha1.IchorInstallationSpec{Version: spec},
		Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: status},
	}).Build()
}

func renderVersion(t *testing.T, info versionInfo, format string) string {
	t.Helper()

	var buf bytes.Buffer
	if err := writeVersion(&buf, format, info); err != nil {
		t.Fatal(err)
	}

	return buf.String()
}

func TestVersionShowsInstalledVersion(t *testing.T) {
	info, note := collectVersion(context.Background(), installationWith(t, testServerV04, testServerV04), testCLIVersion)
	if note != "" {
		t.Fatalf("unexpected note %q", note)
	}

	out := renderVersion(t, info, outputYAML)
	if !strings.Contains(out, "installedVersion: "+testServerV04) || strings.Contains(out, "specVersion") {
		t.Fatalf("output = %q", out)
	}

	if !strings.Contains(renderVersion(t, info, outputJSON), `"installedVersion": "v0.4.0"`) {
		t.Fatal("json output lacks the installed version")
	}
}

func TestVersionShowsSpecDifference(t *testing.T) {
	info, _ := collectVersion(context.Background(), installationWith(t, "v0.5.0", testServerV04), testCLIVersion)

	out := renderVersion(t, info, outputYAML)
	if !strings.Contains(out, "installedVersion: "+testServerV04) || !strings.Contains(out, "specVersion: v0.5.0") {
		t.Fatalf("output = %q", out)
	}
}

func TestVersionUnreachableClusterIsANoteNotAnError(t *testing.T) {
	var reads int

	info, note := collectVersion(context.Background(), failReader{reads: &reads}, testCLIVersion)
	if reads != 1 || note == "" || info.Server != nil || info.Client.Version != testCLIVersion {
		t.Fatalf("reads = %d, info = %+v, note = %q", reads, info, note)
	}
}

func TestVersionClientFlagDoesNotTouchTheCluster(t *testing.T) {
	var out, errOut bytes.Buffer

	versionCmd.SetOut(&out)
	versionCmd.SetErr(&errOut)
	// A kubeconfig that cannot be loaded: --client must not even try.
	kubeconfigPath = "/nonexistent/kubeconfig"
	versionClientOnly = true

	t.Cleanup(func() { kubeconfigPath, versionClientOnly = "", false })

	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatal(err)
	}

	if errOut.Len() != 0 || !strings.HasPrefix(out.String(), "ichor ") || strings.Contains(out.String(), "installed") {
		t.Fatalf("stdout = %q, stderr = %q", out.String(), errOut.String())
	}
}
