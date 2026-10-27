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

package uiapi

import (
	"context"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

func TestIssuerLoginUpgradesAWeakHashAndKeepsTheRest(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, "viewer"))

	weak, err := ichorauth.HashWithParams(strongPass, ichorauth.Params{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16})
	if err != nil {
		t.Fatal(err)
	}

	notBefore := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := s.writeCredential(context.Background(), userAlice,
		ichorauth.Credential{Version: ichorauth.CredentialVersion, Hash: weak, NotBefore: notBefore, MustChange: true}); err != nil {
		t.Fatal(err)
	}

	b := &IssuerBackend{Users: s.Users.Cache, Reader: s.Client, Writer: s.Client, Namespace: usersNS, Settings: s.Users}

	if ok, err := b.VerifyPassword(context.Background(), userAlice, "wrong password!!"); err != nil || ok {
		t.Fatalf("wrong password: %v %v", ok, err)
	}

	if cred, _ := readStored(t, s); cred.Hash != weak {
		t.Fatal("a failed login rewrote the hash")
	}

	if ok, err := b.VerifyPassword(context.Background(), userAlice, strongPass); err != nil || !ok {
		t.Fatalf("login: %v %v", ok, err)
	}

	cred, _ := readStored(t, s)
	if cred.Hash == weak || !pwMatches(strongPass, cred.Hash) {
		t.Error("hash was not upgraded")
	}

	if _, needs, _ := ichorauth.Verify(strongPass, cred.Hash); needs {
		t.Error("the new hash is still weak")
	}

	if !cred.NotBefore.Equal(notBefore) || !cred.MustChange {
		t.Errorf("notBefore/mustChange changed: %+v", cred)
	}
}

func TestIssuerLoginSucceedsWhenTheRehashCannotBeWritten(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, "viewer"))

	weak, err := ichorauth.HashWithParams(strongPass, ichorauth.Params{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.writeCredential(context.Background(), userAlice, ichorauth.Credential{Version: ichorauth.CredentialVersion, Hash: weak}); err != nil {
		t.Fatal(err)
	}

	// No Writer: the hash stays, the login works.
	b := &IssuerBackend{Users: s.Users.Cache, Reader: s.Client, Namespace: usersNS, Settings: s.Users}
	if ok, err := b.VerifyPassword(context.Background(), userAlice, strongPass); err != nil || !ok {
		t.Fatalf("login: %v %v", ok, err)
	}

	if cred, _ := readStored(t, s); cred.Hash != weak {
		t.Error("hash changed without a Writer")
	}
}
