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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0108: the cluster lock is a Lease named after the TalosCluster, written with the controller's
// cluster-wide Lease rights, so no Ichor object may live where the Kubernetes control plane keeps
// its own Leases.
const leaseName = "kube-scheduler"

var _ = Describe("Ichor objects in the Kubernetes system namespaces", func() {
	for _, ns := range []string{"kube-system", "kube-public", "kube-node-lease"} {
		It("refuses a TalosCluster, TalosMachine, TalosEtcdBackup and TalosOperation in "+ns, func() {
			cluster := newTestCluster()
			cluster.Namespace = ns
			cluster.GenerateName = ""
			cluster.Name = leaseName

			err := k8sClient.Create(ctx, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(ns))

			machine := newTestMachine()
			machine.Namespace = ns
			Expect(k8sClient.Create(ctx, machine)).NotTo(Succeed())

			backup := &ichorv1alpha1.TalosEtcdBackup{
				GenerateName: "b-", Namespace: ns,
				Spec: ichorv1alpha1.TalosEtcdBackupSpec{
					ClusterRef: corev1.LocalObjectReference{Name: leaseName},
					Destination: ichorv1alpha1.TalosEtcdBackupDestination{S3: ichorv1alpha1.TalosEtcdBackupS3{
						Endpoint: "https://s3.example.org", Bucket: secrefBucket, Prefix: "etcd",
						CredentialsSecretRef: corev1.LocalObjectReference{Name: "s3"},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, backup)).NotTo(Succeed())

			op := &ichorv1alpha1.TalosOperation{
				GenerateName: "o-", Namespace: ns,
				Spec: ichorv1alpha1.TalosOperationSpec{
					Kind: ichorv1alpha1.TalosOperationKindTalos, ClusterRef: corev1.LocalObjectReference{Name: leaseName},
				},
			}
			Expect(k8sClient.Create(ctx, op)).NotTo(Succeed())
		})
	}
})
