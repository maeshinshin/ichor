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
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	authv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testWho = "alice@example.com"

// oidcBackend is a backend whose /auth/config says enabled (or not).
func oidcBackend(t *testing.T, enabled bool) (apiDeps, *atomic.Int32, *bytes.Buffer) {
	t.Helper()

	k8s := ctrlfake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(append(uiBackendObjects(), strongMachine("strong"))...).Build()

	var forwards atomic.Int32

	deps := backendFor(t, k8s, &forwards, func(mux *http.ServeMux) {
		mux.HandleFunc("/auth/config", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if enabled {
				_, _ = w.Write([]byte(`{"enabled":true,"issuer":"https://idp","clientId":"x"}`))
			} else {
				_, _ = w.Write([]byte(`{"enabled":false}`))
			}
		})
	})

	stderr := &bytes.Buffer{}
	deps.Stderr = stderr
	deps.WhoAmI = func(context.Context) (string, error) { return testWho, nil }

	return deps, &forwards, stderr
}

// approvedBy approves "strong" through c and returns the recorded actor.
func approvedBy(t *testing.T, k8s client.Client, c *apiClients) string {
	t.Helper()

	err := runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong", approveWorker)
	if err != nil {
		t.Fatal(err)
	}

	return getMachine(t, k8s, "strong").Spec.ApprovalDecision.Actor
}

func TestOIDCBackendSwitchesToInProcessAndSaysSo(t *testing.T) {
	setAPITestFlags(t, true)

	deps, forwards, stderr := oidcBackend(t, true)

	c, err := connectAPI(context.Background(), apiOpts, deps, false)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if !c.Local {
		t.Fatal("OIDC enabled: want the in-process clients")
	}

	if !strings.Contains(stderr.String(), "OIDC") || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("want one line naming OIDC, got %q", stderr.String())
	}

	if forwards.Load() != 1 {
		t.Fatalf("want the tunnel used once to ask, got %d", forwards.Load())
	}

	if got := approvedBy(t, deps.K8s, c); got != testWho {
		t.Fatalf("want actor %q, got %q", testWho, got)
	}
}

func TestOIDCDisabledStaysOnTheBackend(t *testing.T) {
	setAPITestFlags(t, true)

	deps, _, _ := oidcBackend(t, false)

	c, err := connectAPI(context.Background(), apiOpts, deps, false)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if c.Local {
		t.Fatal("OIDC disabled: want the backend")
	}
}

func TestOIDCRefusesNodeCommandsWithAReason(t *testing.T) {
	setAPITestFlags(t, true)

	deps, _, _ := oidcBackend(t, true)

	_, err := connectAPI(context.Background(), apiOpts, deps, true)
	if !errors.Is(err, errOIDCBackend) {
		t.Fatalf("want errOIDCBackend, got %v", err)
	}

	if !strings.Contains(err.Error(), "OIDC") || !strings.Contains(err.Error(), "token") {
		t.Fatalf("the reason must say OIDC and tokens, got %v", err)
	}
}

func TestLocalActorFallsBackWhenTheNameIsUnknown(t *testing.T) {
	setAPITestFlags(t, true)

	deps, _, _ := oidcBackend(t, true)
	deps.WhoAmI = func(context.Context) (string, error) { return "", errors.New("forbidden") }

	c, err := connectAPI(context.Background(), apiOptions{Local: true, IchorNamespace: ichorControllerNamespace}, deps, false)
	if err != nil {
		t.Fatal(err)
	}

	if got := approvedBy(t, deps.K8s, c); got != actorCLI {
		t.Fatalf("want %q, got %q", actorCLI, got)
	}
}

func TestSelfSubjectName(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectReview{Status: authv1.SelfSubjectReviewStatus{UserInfo: authv1.UserInfo{Username: testWho}}}, nil
	})

	got, err := selfSubjectName(context.Background(), cs.AuthenticationV1().SelfSubjectReviews())
	if err != nil || got != testWho {
		t.Fatalf("got %q, %v", got, err)
	}
}
