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
	stdx509 "crypto/x509"
	"encoding/pem"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ADR 0108: ichor-ui holds no CA key; it gets certificates and public facts from the controller.
var _ = Describe("credentials from the controller", func() {
	It("renders with the cluster's public certificates and none of its keys or tokens", func() {
		real := newTestBundle()
		cluster := newTestClusterWithBundle()
		s := &Server{Client: k8sClient}

		b, err := s.renderBundle(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		Expect(b.Cluster.ID).To(Equal(real.Cluster.ID))
		Expect(b.Certs.OS.Crt).To(Equal(real.Certs.OS.Crt))
		Expect(b.Certs.K8s.Crt).To(Equal(real.Certs.K8s.Crt))

		for name, pair := range map[string][2][]byte{
			"talos CA key":      {b.Certs.OS.Key, real.Certs.OS.Key},
			"kubernetes CA key": {b.Certs.K8s.Key, real.Certs.K8s.Key},
			"etcd CA key":       {b.Certs.Etcd.Key, real.Certs.Etcd.Key},
			"bootstrap token":   {[]byte(b.Secrets.BootstrapToken), []byte(real.Secrets.BootstrapToken)},
			"cluster secret":    {[]byte(b.Cluster.Secret), []byte(real.Cluster.Secret)},
			"secretbox secret":  {[]byte(b.Secrets.SecretboxEncryptionSecret), []byte(real.Secrets.SecretboxEncryptionSecret)},
		} {
			Expect(pair[0]).NotTo(Equal(pair[1]), name+" must not reach this backend")
			Expect(pair[0]).NotTo(BeEmpty(), name)
		}
	})

	It("gets a short-lived reader identity whose key never left this process, and reuses it", func() {
		cluster := newTestClusterWithBundle()
		s := &Server{Client: k8sClient}

		id, err := s.nodeReader(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(id.NotAfter).To(BeTemporally("~", time.Now().Add(10*time.Minute), time.Minute))

		block, _ := pem.Decode(id.Crt)
		Expect(block).NotTo(BeNil())

		crt, err := stdx509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(crt.Subject.Organization).To(ConsistOf("os:reader"))

		again, err := s.nodeReader(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(BeIdenticalTo(id), "the identity is reused until half its life is gone")
	})
})
