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
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestSetCertificatesExpiring(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	expiry := func(name string, in time.Duration) ichorv1alpha1.CertificateExpiry {
		return ichorv1alpha1.CertificateExpiry{Name: name, NotAfter: metav1.NewTime(now.Add(in))}
	}

	cases := []struct {
		name       string
		expiries   []ichorv1alpha1.CertificateExpiry
		status     metav1.ConditionStatus
		reason     string
		mentions   []string
		mentionNot []string
	}{
		{"far away", []ichorv1alpha1.CertificateExpiry{expiry("talos", 400*day)}, metav1.ConditionFalse, "NoneExpiring", nil, nil},
		{"just outside the window", []ichorv1alpha1.CertificateExpiry{expiry("talos", 91*day)}, metav1.ConditionFalse, "NoneExpiring", nil, nil},
		{
			"one inside", []ichorv1alpha1.CertificateExpiry{expiry("talos", 400*day), expiry("etcd", 45*day)},
			metav1.ConditionTrue, "ExpiringSoon", []string{"the etcd CA expires on 2026-12-11"}, []string{"talos"},
		},
		{
			"expired", []ichorv1alpha1.CertificateExpiry{expiry("kubernetes", -2*day)},
			metav1.ConditionTrue, "Expired", []string{"the kubernetes CA expired on 2026-10-25"}, nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cluster ichorv1alpha1.TalosCluster

			if !setCertificatesExpiring(&cluster, tc.expiries, now, 90*day) {
				t.Fatal("first call reported no change")
			}

			c := apimeta.FindStatusCondition(cluster.Status.Conditions, ichorv1alpha1.ConditionCertificatesExpiring)
			if c == nil || c.Status != tc.status || c.Reason != tc.reason {
				t.Fatalf("condition = %+v, want status %s reason %s", c, tc.status, tc.reason)
			}

			for _, s := range tc.mentions {
				if !strings.Contains(c.Message, s) {
					t.Errorf("message %q lacks %q", c.Message, s)
				}
			}

			for _, s := range tc.mentionNot {
				if strings.Contains(c.Message, s) {
					t.Errorf("message %q should not mention %q", c.Message, s)
				}
			}

			// An hour later nothing changed, so nothing should be written.
			if setCertificatesExpiring(&cluster, tc.expiries, now.Add(time.Hour), 90*day) {
				t.Error("second call a moment later reported a change")
			}
		})
	}
}
