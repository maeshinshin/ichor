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

package ichorauth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const goodPassword = "correct horse battery"

// cheap keeps tests fast; the weaker-than-default shape is what the rehash test needs anyway.
var cheap = Params{Time: 1, Memory: 8, Threads: 1, KeyLen: 32, SaltLen: 16}

func TestHashRoundTripAndEncoding(t *testing.T) {
	h, err := Hash(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("unexpected encoding prefix: %q", h)
	}
	if strings.Contains(h, goodPassword) {
		t.Fatal("hash contains the password")
	}
	ok, rehash, err := Verify(goodPassword, h)
	if err != nil || !ok || rehash {
		t.Fatalf("Verify = %v, %v, %v; want true, false, nil", ok, rehash, err)
	}
	ok, _, err = Verify("another password!", h)
	if err != nil || ok {
		t.Fatalf("wrong password: ok=%v err=%v", ok, err)
	}
	h2, _ := Hash(goodPassword)
	if h == h2 {
		t.Fatal("two hashes of one password are equal: salt is not random")
	}
}

func TestParameterUpgrade(t *testing.T) {
	h, err := HashWithParams(goodPassword, cheap)
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash, err := Verify(goodPassword, h)
	if err != nil || !ok || !rehash {
		t.Fatalf("old-parameter hash: ok=%v rehash=%v err=%v; want true, true, nil", ok, rehash, err)
	}
	ok, rehash, _ = Verify("not the password", h)
	if ok || rehash {
		t.Fatal("a wrong password must neither match nor request a rehash")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	good, _ := HashWithParams(goodPassword, cheap)
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":        "",
		"wrong algo":   strings.Replace(good, "argon2id", "argon2i", 1),
		"bad version":  strings.Replace(good, "v=19", "v=16", 1),
		"huge memory":  "$argon2id$v=19$m=4194304,t=1,p=1$" + parts[4] + "$" + parts[5],
		"huge time":    "$argon2id$v=19$m=8,t=999,p=1$" + parts[4] + "$" + parts[5],
		"zero threads": "$argon2id$v=19$m=8,t=1,p=0$" + parts[4] + "$" + parts[5],
		"unknown key":  "$argon2id$v=19$m=8,t=1,p=1,x=1$" + parts[4] + "$" + parts[5],
		"bad salt":     "$argon2id$v=19$m=8,t=1,p=1$!!$" + parts[5],
		"missing key":  "$argon2id$v=19$m=8,t=1,p=1$" + parts[4],
	}
	for name, enc := range cases {
		if ok, _, err := Verify(goodPassword, enc); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v; want false and an error", name, ok, err)
		}
	}
	_, _, err := Verify(goodPassword, strings.Replace(good, "v=19", "v=16", 1))
	if !errors.Is(err, ErrIncompatibleVersion) {
		t.Errorf("bad version error = %v; want ErrIncompatibleVersion", err)
	}
}

func TestErrorsDoNotLeakSecrets(t *testing.T) {
	secret := "leaky-password-123456"
	_, _, err := Verify(secret, "$argon2id$garbage"+secret)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error %v leaks input", err)
	}
	_, err = ParseCredential([]byte(`{"v":1,"hash":` + secret))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("ParseCredential error %v leaks input", err)
	}
}

func TestVerifyDummyAlwaysFalse(t *testing.T) {
	if VerifyDummy(goodPassword) {
		t.Fatal("VerifyDummy returned true")
	}
}

// The compare must go through the same derive-then-compare path whether the user exists or not:
// a dummy verify costs the same order of work as a real one. Wall-clock equality is not asserted
// (flaky); the shape is: both derive with DefaultParams.
func TestDummyHashUsesDefaultParams(t *testing.T) {
	VerifyDummy("x")
	p, _, _, err := decode(dummyHash())
	if err != nil {
		t.Fatal(err)
	}
	if p.Time != DefaultParams.Time || p.Memory != DefaultParams.Memory || p.Threads != DefaultParams.Threads {
		t.Fatalf("dummy params %+v differ from defaults %+v", p, DefaultParams)
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		want error
	}{
		{"11 characters", strings.Repeat("a", 11), ErrPasswordTooShort},
		{"12 characters", strings.Repeat("a", 12), nil},
		{"12 multibyte characters", strings.Repeat("あ", 12), nil},
		{"11 multibyte characters", strings.Repeat("あ", 11), ErrPasswordTooShort},
		{"128 bytes", strings.Repeat("a", 128), nil},
		{"129 bytes", strings.Repeat("a", 129), ErrPasswordTooLong},
		{"invalid utf-8", strings.Repeat("\xff", 20), ErrPasswordTooShort},
		{"no composition rules", "aaaaaaaaaaaa", nil},
	}
	for _, c := range cases {
		if err := ValidatePassword(c.pw); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v; want %v", c.name, err, c.want)
		}
	}
	if _, err := Hash("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("Hash must apply the policy, got %v", err)
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != GeneratedPasswordLength || ValidatePassword(p) != nil {
			t.Fatalf("generated password %q violates the policy", p)
		}
		seen[p] = true
	}
	if len(seen) != 20 {
		t.Fatal("generated passwords repeat")
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 500, time.UTC)
	c, err := NewCredential(goodPassword, true, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), goodPassword) {
		t.Fatal("credential entry contains the password")
	}
	got, err := ParseCredential(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MustChange || got.Version != 1 || !got.NotBefore.Equal(now.Truncate(time.Second)) {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if ok, _, err := got.Verify(goodPassword); err != nil || !ok {
		t.Fatalf("Verify after round trip: %v %v", ok, err)
	}
	for _, bad := range []string{`{}`, `{"v":2,"hash":"x"}`, `{"v":1}`, `nope`} {
		if _, err := ParseCredential([]byte(bad)); err == nil {
			t.Errorf("ParseCredential(%q) accepted", bad)
		}
	}
}

func TestNamesAndRoles(t *testing.T) {
	for _, n := range []string{"a", "alice", "a-b-1", strings.Repeat("a", 63)} {
		if err := ValidateUserName(n); err != nil {
			t.Errorf("ValidateUserName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"", "1a", "A", "a_b", "a.b", "a-", "system:x", strings.Repeat("a", 64)} {
		if ValidateUserName(n) == nil {
			t.Errorf("ValidateUserName(%q) accepted", n)
		}
	}
	for _, r := range []string{"admin", "ops-team", "a"} {
		if err := ValidateRole(r); err != nil {
			t.Errorf("ValidateRole(%q) = %v", r, err)
		}
	}
	for _, r := range []string{"", "Admin", "ichor:admin", "system:masters", "-a", strings.Repeat("a", 32)} {
		if ValidateRole(r) == nil {
			t.Errorf("ValidateRole(%q) accepted", r)
		}
	}
}
