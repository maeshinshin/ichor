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
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0124: the os:reader talosconfig a person takes. The verb is `download` on
// talosclusters/talosconfig-reader, the lifetime is capped at admission, and an Event naming the user is
// written before the request is admitted.
func TestTalosconfigReaderRequest(t *testing.T) {
	t.Parallel()

	grants := map[string][]string{
		"ichor:alice": {"download talosclusters/talosconfig-reader"},
		"ichor:rot":   {"download talosclusters/credentials"},
		"sa":          {"issue talosclusters/public", "issue talosclusters/node-reader"},
	}

	answer := func(sar *authzv1.SubjectAccessReview) bool {
		ra := sar.Spec.ResourceAttributes

		return slices.Contains(grants[sar.Spec.User], ra.Verb+" "+ra.Resource+"/"+ra.Subresource)
	}

	handle := func(h *CredentialRequestHandler, user string, ttl time.Duration, dryRun bool) admission.Response {
		spec := ichorv1alpha1.TalosCredentialRequestSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"}, Purpose: ichorv1alpha1.CredentialPurposeTalosconfigReader, CSR: "x",
		}
		if ttl != 0 {
			spec.TTL = &metav1.Duration{Duration: ttl}
		}

		raw, err := json.Marshal(ichorv1alpha1.TalosCredentialRequest{Spec: spec})
		if err != nil {
			t.Fatal(err)
		}

		return h.Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create, Namespace: "ns", UserInfo: authnv1.UserInfo{Username: user}, DryRun: &dryRun,
			Object: runtime.RawExtension{Raw: raw},
		}})
	}

	events := func(c client.Client) []corev1.Event {
		var list corev1.EventList
		if err := c.List(context.Background(), &list); err != nil {
			t.Fatal(err)
		}

		return list.Items
	}

	t.Run("a holder of the verb is admitted and the Event names the user", func(t *testing.T) {
		t.Parallel()

		c := sarClient(t, answer)
		h := &CredentialRequestHandler{Client: c}

		if got := handle(h, "ichor:alice", 2*time.Hour, false); !got.Allowed {
			t.Fatalf("denied: %v", got.Result)
		}

		evs := events(c)
		if len(evs) != 1 {
			t.Fatalf("got %d Events, want 1", len(evs))
		}

		ev := evs[0]
		if ev.Reason != "TalosconfigReader" || ev.InvolvedObject.Kind != "TalosCluster" || ev.InvolvedObject.Name != "c1" ||
			ev.Namespace != "ns" || !strings.Contains(ev.Message, "ichor:alice") || !strings.Contains(ev.Message, "2h0m0s") {
			t.Errorf("unexpected Event %+v", ev)
		}
	})

	t.Run("the default lifetime is named when none is asked for", func(t *testing.T) {
		t.Parallel()

		c := sarClient(t, answer)
		if got := handle(&CredentialRequestHandler{Client: c}, "ichor:alice", 0, false); !got.Allowed {
			t.Fatalf("denied: %v", got.Result)
		}

		if evs := events(c); len(evs) != 1 || !strings.Contains(evs[0].Message, "1h0m0s") {
			t.Errorf("Events %+v", evs)
		}
	})

	t.Run("anyone else is refused and nothing is recorded", func(t *testing.T) {
		t.Parallel()

		for _, user := range []string{"ichor:rot", "sa", "nobody"} {
			c := sarClient(t, answer)
			if got := handle(&CredentialRequestHandler{Client: c}, user, time.Hour, false); got.Allowed {
				t.Errorf("%s was admitted", user)
			}

			if evs := events(c); len(evs) != 0 {
				t.Errorf("%s: %d Events written for a refused request", user, len(evs))
			}
		}
	})

	t.Run("more than twelve hours is refused before anything is recorded", func(t *testing.T) {
		t.Parallel()

		c := sarClient(t, answer)
		if got := handle(&CredentialRequestHandler{Client: c}, "ichor:alice", 12*time.Hour+time.Second, false); got.Allowed {
			t.Error("a 12h1s talosconfig was admitted")
		}

		if got := handle(&CredentialRequestHandler{Client: c}, "ichor:alice", 12*time.Hour, false); !got.Allowed {
			t.Errorf("exactly 12h was refused: %v", got.Result)
		}

		if evs := events(c); len(evs) != 1 {
			t.Errorf("got %d Events, want 1 (only the admitted one)", len(evs))
		}
	})

	t.Run("a dry run writes no Event", func(t *testing.T) {
		t.Parallel()

		c := sarClient(t, answer)
		if got := handle(&CredentialRequestHandler{Client: c}, "ichor:alice", time.Hour, true); !got.Allowed {
			t.Fatalf("denied: %v", got.Result)
		}

		if evs := events(c); len(evs) != 0 {
			t.Errorf("a dry run wrote %d Events", len(evs))
		}
	})

	t.Run("a request that cannot be recorded is refused", func(t *testing.T) {
		t.Parallel()

		c := fake.NewClientBuilder().WithScheme(schemeOf(t, scheme.AddToScheme)).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				switch o := obj.(type) {
				case *authzv1.SubjectAccessReview:
					o.Status.Allowed = answer(o)

					return nil
				case *corev1.Event:
					return errors.New("events are down")
				}

				return cl.Create(ctx, obj, opts...)
			},
		}).Build()

		if got := handle(&CredentialRequestHandler{Client: c}, "ichor:alice", time.Hour, false); got.Allowed {
			t.Error("admitted although the Event could not be written")
		}
	})
}
