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

package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/go-jose/go-jose/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	secretsResource = "secrets"
	tokenKey        = "token"
)

func secretIn(ns, name string, typ corev1.SecretType, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{Namespace: ns, Name: name, ResourceVersion: "1", Type: typ, Data: data}
}

// fakeStore makes the fake clientset behave like kube-apiserver in the one respect a re-encryption depends on:
// updating a Secret whose name is in stale stores it again (a new resourceVersion), updating any other is a no-op.
func fakeStore(cs *fake.Clientset, stale map[string]bool, conflict map[string]bool) {
	cs.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)

		if conflict[secret.Name] {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: secretsResource}, secret.Name, errors.New("changed"))
		}

		if stale[secret.Name] {
			delete(stale, secret.Name)

			n, _ := strconv.Atoi(secret.ResourceVersion)
			out := secret.DeepCopy()
			out.ResourceVersion = strconv.Itoa(n + 1)

			return true, out, nil
		}

		return true, secret.DeepCopy(), nil
	})
}

func TestReencryptSweepRewritesOnlyWhatTheServerStoredAgain(t *testing.T) {
	cs := fake.NewSimpleClientset(
		secretIn("a", "one", corev1.SecretTypeOpaque, map[string][]byte{"k": []byte("v1")}),
		secretIn("a", "two", corev1.SecretTypeOpaque, map[string][]byte{"k": []byte("v2")}),
		secretIn("b", "three", corev1.SecretTypeOpaque, map[string][]byte{"k": []byte("v3")}),
	)
	fakeStore(cs, map[string]bool{"one": true, "three": true}, nil)

	first, err := reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil {
		t.Fatal(err)
	}

	if first.Scanned != 3 || first.Rewritten != 2 || first.Conflicts != 0 {
		t.Fatalf("first sweep: %+v", first)
	}

	// A sweep that finds nothing stale is the proof the stage waits for.
	second, err := reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil {
		t.Fatal(err)
	}

	if second.Scanned != 3 || second.Rewritten != 0 || second.Conflicts != 0 {
		t.Fatalf("second sweep: %+v", second)
	}

	// Every update carried the Secret's own content, nothing else.
	for _, a := range cs.Actions() {
		u, ok := a.(k8stesting.UpdateAction)
		if !ok {
			continue
		}

		got := u.GetObject().(*corev1.Secret)
		if want := map[string]string{"one": "v1", "two": "v2", "three": "v3"}[got.Name]; string(got.Data["k"]) != want {
			t.Fatalf("secret %s was rewritten with other content", got.Name)
		}
	}
}

func TestReencryptSweepLeavesConflictsAndNotFoundsToTheNextSweep(t *testing.T) {
	cs := fake.NewSimpleClientset(
		secretIn("a", "busy", corev1.SecretTypeOpaque, nil),
		secretIn("a", "gone", corev1.SecretTypeOpaque, nil),
		secretIn("a", "fine", corev1.SecretTypeOpaque, nil),
	)
	fakeStore(cs, nil, map[string]bool{"busy": true})
	cs.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if s := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret); s.Name == "gone" {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: secretsResource}, s.Name)
		}

		return false, nil, nil
	})

	got, err := reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil {
		t.Fatal(err)
	}

	// A Secret that changed under the sweep was not looked at; the sweep is not clean.
	if got.Conflicts != 1 || got.Rewritten != 0 || got.Clean() {
		t.Fatalf("%+v", got)
	}

	cs = fake.NewSimpleClientset(secretIn("a", "fine", corev1.SecretTypeOpaque, nil))
	fakeStore(cs, nil, nil)

	got, err = reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil || !got.Clean() {
		t.Fatalf("a sweep with nothing stale and no conflict is clean: %+v %v", got, err)
	}
}

func TestReencryptSweepFailsOnAnError(t *testing.T) {
	cs := fake.NewSimpleClientset(secretIn("a", "one", corev1.SecretTypeOpaque, nil))
	cs.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: secretsResource}, "one", errors.New("no"))
	})

	if _, err := reencryptSweep(context.Background(), cs, ReencryptResult{}); err == nil {
		t.Fatal("an error other than a conflict or a missing Secret must stop the sweep")
	}
}

func pemPublic(t *testing.T, pub any) []byte {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func mintToken(t *testing.T, alg jose.SignatureAlgorithm, key any) string {
	t.Helper()

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}

	sig, err := signer.Sign([]byte(`{"iss":"https://kubernetes.default.svc","sub":"system:serviceaccount:a:b"}`))
	if err != nil {
		t.Fatal(err)
	}

	token, err := sig.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}

	return token
}

func TestLegacyTokensSignedByFindsOnlyTheOnesTheOldKeyVerifies(t *testing.T) {
	oldRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	oldEC, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	newEC, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tokenType := corev1.SecretTypeServiceAccountToken

	for name, tc := range map[string]struct {
		oldPub any
		signed string
		want   []string
	}{
		"rsa": {&oldRSA.PublicKey, mintToken(t, jose.RS256, oldRSA), []string{"a/signed-by-old", "b/also-old"}},
		"ec":  {&oldEC.PublicKey, mintToken(t, jose.ES256, oldEC), []string{"a/signed-by-old", "b/also-old"}},
	} {
		t.Run(name, func(t *testing.T) {
			cs := fake.NewSimpleClientset(
				secretIn("a", "signed-by-old", tokenType, map[string][]byte{tokenKey: []byte(tc.signed)}),
				secretIn("b", "also-old", tokenType, map[string][]byte{tokenKey: []byte(tc.signed)}),
				secretIn("a", "signed-by-new", tokenType, map[string][]byte{tokenKey: []byte(mintToken(t, jose.ES256, newEC))}),
				secretIn("a", "no-token-yet", tokenType, nil),
				secretIn("a", "garbage", tokenType, map[string][]byte{tokenKey: []byte("not.a.jwt")}),
				// a Secret of another type is not a token whatever it holds
				secretIn("a", "opaque", corev1.SecretTypeOpaque, map[string][]byte{tokenKey: []byte(tc.signed)}),
			)

			got, err := legacyTokensSignedBy(context.Background(), cs, pemPublic(t, tc.oldPub))
			if err != nil {
				t.Fatal(err)
			}

			slices.Sort(got)

			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	if _, err := legacyTokensSignedBy(context.Background(), fake.NewSimpleClientset(), []byte("not a key")); err == nil {
		t.Fatal("a public key that is not PEM must be an error")
	}
}

// pagedSecrets serves n Secrets, in order, the way the API server pages a list: at most Limit per call, and the
// continue token is the index of the next Secret. expireAt, when set, makes the list with that token answer 410
// Gone once.
type pagedSecrets struct {
	lists     int
	maxServed int
	starts    []string
	expireAt  string
	updates   map[string]int
}

func newPagedClient(n int, p *pagedSecrets) *fake.Clientset {
	cs := fake.NewSimpleClientset()
	p.updates = map[string]int{}

	cs.PrependReactor("list", secretsResource, func(action k8stesting.Action) (bool, runtime.Object, error) {
		opts := action.(k8stesting.ListActionImpl).ListOptions
		p.lists++
		p.starts = append(p.starts, opts.Continue)

		if opts.Continue != "" && opts.Continue == p.expireAt {
			p.expireAt = ""

			return true, nil, apierrors.NewResourceExpired("the continue token has expired")
		}

		from, _ := strconv.Atoi(opts.Continue)
		to := min(n, from+int(opts.Limit))
		out := &corev1.SecretList{}

		for i := from; i < to; i++ {
			out.Items = append(out.Items, corev1.Secret{Namespace: "ns", Name: "s" + strconv.Itoa(i), ResourceVersion: "1"})
		}

		if to < n {
			out.Continue = strconv.Itoa(to)
		}

		p.maxServed = max(p.maxServed, len(out.Items))

		return true, out, nil
	})
	cs.PrependReactor("update", secretsResource, func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		p.updates[secret.Name]++

		return true, secret.DeepCopy(), nil
	})

	return cs
}

func TestReencryptSweepPagesEverySecret(t *testing.T) {
	const total = 3*reencryptPage + 7

	var p pagedSecrets

	cs := newPagedClient(total, &p)

	got, err := reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil {
		t.Fatal(err)
	}

	if got.Scanned != total || got.Continue != "" {
		t.Fatalf("a sweep of %d Secrets: %+v", total, got)
	}

	if len(p.updates) != total {
		t.Fatalf("%d Secrets were swept, want %d", len(p.updates), total)
	}

	if p.maxServed > reencryptPage || p.lists != 4 {
		t.Fatalf("list calls %d (want 4), largest page %d (want at most %d)", p.lists, p.maxServed, reencryptPage)
	}
}

func TestReencryptSweepStopsAfterItsPagesAndResumesFromTheToken(t *testing.T) {
	const total = reencryptPagesPerCall*reencryptPage + 30

	var p pagedSecrets

	cs := newPagedClient(total, &p)

	first, err := reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil {
		t.Fatal(err)
	}

	if first.Continue == "" || first.Scanned != reencryptPagesPerCall*reencryptPage || p.lists != reencryptPagesPerCall {
		t.Fatalf("one call does at most %d pages and keeps the position: %+v after %d lists", reencryptPagesPerCall, first, p.lists)
	}

	// A new call, as after a restart, is given only what status kept.
	p.lists, p.starts = 0, nil

	second, err := reencryptSweep(context.Background(), cs, first)
	if err != nil {
		t.Fatal(err)
	}

	if p.lists != 1 || p.starts[0] != first.Continue {
		t.Fatalf("the resumed call must list once, from %q: %d lists starting at %v", first.Continue, p.lists, p.starts)
	}

	if second.Continue != "" || second.Scanned != total {
		t.Fatalf("resumed sweep: %+v", second)
	}

	for name, n := range p.updates {
		if n != 1 {
			t.Fatalf("Secret %s was updated %d times; resuming must not reprocess", name, n)
		}
	}
}

func TestReencryptSweepStartsOverWhenTheContinueTokenExpired(t *testing.T) {
	const total = 2*reencryptPage + 5

	var p pagedSecrets

	cs := newPagedClient(total, &p)
	p.expireAt = strconv.Itoa(reencryptPage)

	got, err := reencryptSweep(context.Background(), cs, ReencryptResult{})
	if err != nil {
		t.Fatal(err)
	}

	// Pages: 0, 500 (410), then from the start again: 0, 500, 1000.
	if p.starts[len(p.starts)-3] != "" || got.Continue != "" || got.Scanned != total {
		t.Fatalf("after a 410 the sweep restarts from the beginning and finishes: %+v, list starts %q", got, p.starts)
	}

	if len(p.updates) != total {
		t.Fatalf("%d Secrets swept, want %d", len(p.updates), total)
	}
}
