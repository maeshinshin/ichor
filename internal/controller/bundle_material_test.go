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
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The scrubber must cover keys the old hand-written list missed.
var _ = Describe("bundleSecretMaterial", func() {
	It("covers every key of the bundle", func() {
		// newTestBundle hands out one bundle to the whole suite: set the two secrets on a copy, or every spec that runs
		// after this one renders a cluster that also has an aescbc provider.
		b := *newTestBundle("v1.14")
		secs := *b.Secrets
		secs.SecretboxEncryptionSecret = "secretbox-value"
		secs.AESCBCEncryptionSecret = "aescbc-value"
		b.Secrets = &secs

		got := bundleSecretMaterial(&b)

		for _, want := range []string{
			string(b.Certs.Etcd.Key), string(b.Certs.K8sAggregator.Key), string(b.Certs.K8sServiceAccount.Key),
			"secretbox-value", "aescbc-value", string(b.Certs.OS.Key), b.TrustdInfo.Token,
		} {
			if want != "" {
				Expect(slices.Contains(got, strings.TrimSpace(want))).To(BeTrue())
			}
		}
	})
})
