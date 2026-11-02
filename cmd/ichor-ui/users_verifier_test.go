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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

type staticKeys []jose.JSONWebKey

func (s staticKeys) Public() []jose.JSONWebKey { return s }

func TestLocalVerifierChecksIssuerAudienceAndSignature(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	v := localVerifier{keys: staticKeys{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}, issuer: testIssuer}

	mint := func(signer *rsa.PrivateKey, iss, aud string, exp time.Time) string {
		sg, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: signer, KeyID: "k1"}}, nil)
		if err != nil {
			t.Fatal(err)
		}

		payload, _ := json.Marshal(map[string]any{"iss": iss, "aud": aud, "sub": "alice", "iat": time.Now().Unix(), "exp": exp.Unix()})

		obj, err := sg.Sign(payload)
		if err != nil {
			t.Fatal(err)
		}

		tok, _ := obj.CompactSerialize()

		return tok
	}

	future := time.Now().Add(time.Minute)

	if sub, _, err := v.Verify(context.Background(), mint(key, testIssuer, "ichor-ui", future)); err != nil || sub != "alice" {
		t.Fatalf("valid token: %q %v", sub, err)
	}

	for name, tok := range map[string]string{
		"kube audience": mint(key, testIssuer, "ichor-kube", future),
		"other issuer":  mint(key, "https://evil", "ichor-ui", future),
		"other key":     mint(other, testIssuer, "ichor-ui", future),
		"expired":       mint(key, testIssuer, "ichor-ui", time.Now().Add(-time.Hour)),
	} {
		if _, _, err := v.Verify(context.Background(), tok); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
