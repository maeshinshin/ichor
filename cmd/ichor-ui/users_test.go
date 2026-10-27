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
	"testing"
	"time"
)

const (
	testIssuer = "https://ichor.example.com"
	testIDP    = "https://idp"
)

func TestAuthConfigProviders(t *testing.T) {
	on := usersFlags{enabled: true, externalURL: testIssuer}

	cases := []struct {
		name      string
		oidc      string
		users     usersFlags
		enabled   bool
		providers []string
		issuer    string
	}{
		{"neither", "", usersFlags{}, false, nil, ""},
		{"external only keeps the old keys", testIDP, usersFlags{}, true, []string{"oidc"}, testIDP},
		{"ichor only", "", on, true, []string{"ichor"}, testIssuer},
		{"both", testIDP, on, true, []string{"oidc", "ichor"}, testIDP},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := authConfig(tc.oidc, "client", tc.users)
			if got["enabled"] != tc.enabled || got["issuer"] != tc.issuer {
				t.Fatalf("enabled=%v issuer=%v", got["enabled"], got["issuer"])
			}

			ps := got["providers"].([]authProvider)
			if len(ps) != len(tc.providers) {
				t.Fatalf("providers = %v", ps)
			}

			for i, id := range tc.providers {
				if ps[i].ID != id {
					t.Errorf("provider %d = %q, want %q", i, ps[i].ID, id)
				}
			}
		})
	}
}

func TestUsersFlagsValidate(t *testing.T) {
	ok := usersFlags{enabled: true, externalURL: testIssuer, tokenTTL: 5 * time.Minute, refreshTTL: 12 * time.Hour}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}

	if err := (usersFlags{}).validate(); err != nil {
		t.Fatalf("off must always validate: %v", err)
	}

	for name, mod := range map[string]func(*usersFlags){
		"http url":      func(f *usersFlags) { f.externalURL = "http://ichor.example.com" },
		"no url":        func(f *usersFlags) { f.externalURL = "" },
		"url with path": func(f *usersFlags) { f.externalURL = "https://x/?a=1" },
		"short ttl":     func(f *usersFlags) { f.tokenTTL = time.Second },
		"long ttl":      func(f *usersFlags) { f.tokenTTL = time.Hour },
		"short refresh": func(f *usersFlags) { f.refreshTTL = time.Minute },
	} {
		f := ok
		mod(&f)

		if f.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
