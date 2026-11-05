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
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const testInstallerDigest = "sha256:4339be3ecfa37a09beb10951b5b91d87052e4081603494bd244cc9cc85cc13ba"

// A fresh `ichor init` has no status.installerImage, so the controller would resolve the installer's tag in
// the registry to run the Cilium Job, which a demo's cluster cannot reach (hardware lane J, P2). init records
// the digest it was told in spec.installerDigest, and the skip of the signature check, when asked.
func TestCreateIchorInstallation_RecordsTheInstallerPin(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newIchorInstallationFake(t)

	if created, err := createIchorInstallation(context.Background(), c, &bytes.Buffer{}, 0,
		installerPin{Digest: testInstallerDigest, SkipSignature: true}); err != nil || !created {
		t.Fatalf("createIchorInstallation = %v, %v", created, err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.InstallerDigest != testInstallerDigest {
		t.Errorf("spec.installerDigest = %q, want %q", got.Spec.InstallerDigest, testInstallerDigest)
	}

	if sig := got.Spec.InstallerSignature; sig == nil || sig.Mode != ichorv1alpha1.InstallerSignatureSkip {
		t.Errorf("spec.installerSignature = %+v, want mode skip", sig)
	}
}

// Without the flags the spec is what it was: the tag is resolved and the signature required.
func TestCreateIchorInstallation_WithoutAPinLeavesTheInstallerToTheController(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newIchorInstallationFake(t)

	if _, err := createIchorInstallation(context.Background(), c, &bytes.Buffer{}, 0, installerPin{}); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.InstallerDigest != "" || got.Spec.InstallerSignature != nil {
		t.Errorf("spec = %+v, want no installer digest and no signature policy", got.Spec)
	}
}

// A skipped signature check is shown, never silent (ADR 0122).
func TestCreateIchorInstallation_SkipIsWarnedAbout(t *testing.T) {
	setVersion(t, testReleasedVersion)

	var warn bytes.Buffer

	if _, err := createIchorInstallation(context.Background(), newIchorInstallationFake(t), &warn, time.Second,
		installerPin{SkipSignature: true}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(warn.String(), "NOT checked") {
		t.Errorf("no warning about the skipped signature check:\n%s", warn.String())
	}
}

func TestInitInstallerFlags(t *testing.T) {
	var (
		o    initOptions
		wait time.Duration
	)

	flags := pflag.NewFlagSet("init", pflag.ContinueOnError)
	bindInitFlags(flags, &o, &wait)

	if err := flags.Parse([]string{"--installer-digest", testInstallerDigest, "--skip-signature-check"}); err != nil {
		t.Fatalf("parsing the installer flags: %v", err)
	}

	if got := o.installerPin(); got != (installerPin{Digest: testInstallerDigest, SkipSignature: true}) {
		t.Errorf("installerPin = %+v", got)
	}

	if err := validateInitInstaller(o); err != nil {
		t.Errorf("a valid digest was refused: %v", err)
	}

	for _, bad := range []string{"sha256:abc", "not-a-digest", strings.ToUpper(testInstallerDigest)} {
		if err := validateInitInstaller(initOptions{InstallerDigest: bad}); err == nil {
			t.Errorf("--installer-digest %q was accepted", bad)
		}
	}
}
