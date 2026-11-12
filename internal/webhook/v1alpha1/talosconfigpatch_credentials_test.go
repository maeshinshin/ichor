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

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	credHost     = "registry.example.com"
	credKeyPEM   = "-----BEGIN PRIVATE KEY-----\nQUJD\n-----END PRIVATE KEY-----\n"
	credHeader   = "Bearer s3cret"
	credManifest = "https://example.invalid/x.yaml"
	credAuth     = "Authorization"
	credToken    = "X-Token"
)

func credRegistry(identity *typedconfig.PEMEncodedCertificateAndKey) *typedconfig.Config {
	skip := true

	return &typedconfig.Config{
		ConfigVersion: dryRenderVersion,
		MachineConfig: &typedconfig.MachineConfig{MachineRegistries: &typedconfig.RegistriesConfig{
			RegistryConfig: map[string]*typedconfig.RegistryConfig{credHost: {
				RegistryTLS: &typedconfig.RegistryTLSConfig{TLSClientIdentity: identity, TLSInsecureSkipVerify: &skip},
			}},
		}},
	}
}

func credHeaders(headers map[string]string) *typedconfig.Config {
	return &typedconfig.Config{
		ConfigVersion: dryRenderVersion,
		ClusterConfig: &typedconfig.ClusterConfig{
			ExtraManifests:       []string{credManifest},
			ExtraManifestHeaders: headers,
		},
	}
}

func TestTalosConfigPatchRefusesCredentials(t *testing.T) {
	ctx := context.Background()
	v := dryRenderValidator(t)

	identity := &typedconfig.PEMEncodedCertificateAndKey{Crt: []byte("crt"), Key: []byte(credKeyPEM)}
	idPath := "spec.documents[0].config.machine.registries.config." + credHost + ".tls.clientIdentity"
	hdrPath := "spec.documents[0].config.cluster.extraManifestHeaders"

	both := credRegistry(identity)
	both.ClusterConfig = credHeaders(map[string]string{credAuth: credHeader}).ClusterConfig

	for _, tc := range []struct {
		name string
		cfg  *typedconfig.Config
		want []string // fields the refusal names; none means admitted
	}{
		{"registry client identity", credRegistry(identity), []string{idPath}},
		{"extraManifestHeaders", credHeaders(map[string]string{credAuth: credHeader}), []string{hdrPath}},
		{"both", both, []string{idPath, hdrPath}},
		{"registry config without an identity", credRegistry(nil), nil},
		{"extraManifests without headers", credHeaders(nil), nil},
	} {
		t.Run("create: "+tc.name, func(t *testing.T) {
			_, err := v.ValidateCreate(ctx, configPatch("p", tc.cfg))
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("want admitted, got %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("want a refusal naming %v, got none", tc.want)
			}

			for _, f := range tc.want {
				if !strings.Contains(err.Error(), f) {
					t.Errorf("refusal %q does not name %q", err, f)
				}
			}

			if !strings.Contains(err.Error(), "secrets bundle") {
				t.Errorf("refusal %q does not say where credentials belong", err)
			}

			if strings.Contains(err.Error(), credHeader) || strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Errorf("refusal %q repeats the credential", err)
			}
		})
	}

	// A value stored before the check stays: an update refuses only what it adds or changes.
	two := map[string]string{credAuth: credHeader, credToken: "t"}
	stored := configPatch("p", credHeaders(two))

	for _, tc := range []struct {
		name    string
		updated *typedconfig.Config
		refused bool
	}{
		{"unchanged", credHeaders(two), false},
		{"removed", credHeaders(nil), false},
		{"one header dropped, one kept", credHeaders(map[string]string{credAuth: credHeader}), false},
		{"one header changed", credHeaders(map[string]string{credAuth: "Bearer other", credToken: "t"}), true},
		{"another added", credHeaders(map[string]string{credAuth: credHeader, credToken: "t", "X-Other": "o"}), true},
		{"identity added", credRegistry(identity), true},
	} {
		t.Run("update: "+tc.name, func(t *testing.T) {
			_, err := v.ValidateUpdate(ctx, stored, configPatch("p", tc.updated))
			if tc.refused != (err != nil) {
				t.Fatalf("refused = %v, want %v (error %v)", err != nil, tc.refused, err)
			}
		})
	}
}
