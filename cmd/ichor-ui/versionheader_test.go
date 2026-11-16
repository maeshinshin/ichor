package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/internal/uiapi"
	"github.com/maeshinshin/ichor/internal/version"
)

const (
	testLocalIssuer = "https://ichor.test"
	testVersion     = "v9.9.9-version-test"
)

// fakeLocalVerifier stands in for the in-process verifier of Ichor's own issuer. It accepts any token and
// names one subject; the signature check belongs to the verifier and is tested in internal/uiapi.
type fakeLocalVerifier struct{}

func (fakeLocalVerifier) Verify(context.Context, string) (uiapi.TokenInfo, error) {
	now := time.Now()

	return uiapi.TokenInfo{Subject: "dana", IssuedAt: now, Expires: now.Add(time.Hour)}, nil
}

// userReader serves the one IchorUser the token names and no credentials Secret, which is what a fresh install has.
type userReader struct {
	client.Reader
}

func (userReader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if u, ok := obj.(*ichorv1alpha1.IchorUser); ok {
		u.Name = key.Name

		return nil
	}

	return apierrors.NewNotFound(corev1.Resource("secrets"), key.Name)
}

// localSignIn is an Authenticator that accepts a token from Ichor's own issuer, built from exported parts only.
func localSignIn() *uiapi.Authenticator {
	return (*uiapi.Authenticator)(nil).WithLocal(testLocalIssuer, fakeLocalVerifier{},
		&uiapi.UserCache{Reader: userReader{}, Namespace: "ichor-system"})
}

// localToken is a bearer token whose unverified payload names the local issuer: the Authenticator routes on
// that claim, and the fake verifier does not read the signature.
func localToken() string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + testLocalIssuer + `"}`))

	return "Bearer h." + payload + ".s"
}

// The version header arrives on an authenticated call through the assembled RPC server (ADR 0059, as amended).
func TestVersionHeaderArrivesOnAnAuthenticatedRPC(t *testing.T) {
	saved := version.Version
	t.Cleanup(func() { version.Version = saved })
	version.Version = testVersion

	mux := http.NewServeMux()
	mountRPC(mux, &uiapi.Server{}, localSignIn())

	req := httptest.NewRequest(http.MethodPost, ichorv1alpha1connect.UserServiceWhoAmIProcedure, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", localToken())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("WhoAmI: status %d, body %q: the authenticated call did not succeed, so the fixture is wrong", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("X-Ichor-Version"); got != testVersion {
		t.Errorf("authenticated RPC: X-Ichor-Version = %q, want %q", got, testVersion)
	}
}
