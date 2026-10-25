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
	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var _ = Describe("ClusterService", func() {
	It("lists every cluster it can see", func() {
		cluster := newTestCluster("does-not-exist")

		s := &Server{Client: k8sClient}
		resp, err := s.ListClusters(ctx, connect.NewRequest(&pb.ListClustersRequest{}))
		Expect(err).NotTo(HaveOccurred())

		found := false

		for _, c := range resp.Msg.GetClusters() {
			if c.GetRef().GetName() == cluster.Name {
				found = true

				Expect(c.GetTalosVersion()).To(Equal(cluster.Spec.TalosVersion))
				Expect(c.GetControlPlaneSchedulable()).To(BeTrue())
			}
		}

		Expect(found).To(BeTrue(), "ListClusters did not return the cluster just created")
	})

	It("gets one cluster by ref", func() {
		cluster := newTestCluster("does-not-exist")

		s := &Server{Client: k8sClient}
		resp, err := s.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name),
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetCluster().GetRef().GetName()).To(Equal(cluster.Name))
	})

	It("reports CodeNotFound for a missing cluster", func() {
		s := &Server{Client: k8sClient}
		_, err := s.GetCluster(ctx, connect.NewRequest(&pb.GetClusterRequest{
			Cluster: objectRef(testNamespace, "no-such-cluster"),
		}))
		Expect(err).To(HaveOccurred())
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
	})
})
