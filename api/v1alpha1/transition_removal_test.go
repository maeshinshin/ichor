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
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// guardedTransitionOwners lists the optional nodes that carry a transition rule (one comparing oldSelf).
// A CEL rule on a node does not run when a JSON patch removes that node, so each entry names the webhook
// check that refuses the removal; the webhook tests in internal/webhook/v1alpha1 pin that refusal.
var guardedTransitionOwners = map[string]string{
	"TalosCluster.spec.cni":     "validateNotRemoved, internal/webhook/v1alpha1/taloscluster_webhook.go",
	"TalosCluster.spec.network": "validateNotRemoved, internal/webhook/v1alpha1/taloscluster_webhook.go",
}

// collectTransitionOwners records every schema node under path that carries a transition rule and can be
// absent from an object (removable), keyed by kind and path. A required node cannot be removed, so the
// schema refuses that removal before any transition rule would be skipped.
func collectTransitionOwners(kind, path string, s *apiextensionsv1.JSONSchemaProps, removable bool, found map[string]bool) {
	if removable && path != "" {
		for _, rule := range s.XValidations {
			if strings.Contains(rule.Rule, "oldSelf") {
				found[kind+path] = true
				break
			}
		}
	}

	for name, child := range s.Properties {
		collectTransitionOwners(kind, path+"."+name, &child, !slices.Contains(s.Required, name), found)
	}

	if s.Items != nil && s.Items.Schema != nil {
		collectTransitionOwners(kind, path+"[]", s.Items.Schema, true, found)
	}

	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		collectTransitionOwners(kind, path+"{}", s.AdditionalProperties.Schema, true, found)
	}
}

var _ = Describe("Transition rules and removal", func() {
	Describe("every transition rule sits on a required node or a guarded one", func() {
		It("finds no removable node with a transition rule outside the guarded list", func() {
			files, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "*.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(files).NotTo(BeEmpty())

			found := map[string]bool{}
			for _, file := range files {
				f, err := os.Open(file)
				Expect(err).NotTo(HaveOccurred())

				crd := &apiextensionsv1.CustomResourceDefinition{}
				decodeErr := k8syaml.NewYAMLOrJSONDecoder(f, 4096).Decode(crd)
				Expect(f.Close()).To(Succeed())
				Expect(decodeErr).NotTo(HaveOccurred(), file)

				for _, v := range crd.Spec.Versions {
					if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
						collectTransitionOwners(crd.Spec.Names.Kind, "", v.Schema.OpenAPIV3Schema, false, found)
					}
				}
			}

			var unguarded []string
			for owner := range found {
				if _, ok := guardedTransitionOwners[owner]; !ok {
					unguarded = append(unguarded, owner)
				}
			}
			slices.Sort(unguarded)
			Expect(unguarded).To(BeEmpty(), "a removable node carries a transition rule that a JSON patch can skip; make the node required or guard its removal in the webhook and list it in guardedTransitionOwners")

			var stale []string
			for owner := range guardedTransitionOwners {
				if !found[owner] {
					stale = append(stale, owner)
				}
			}
			Expect(stale).To(BeEmpty(), "guardedTransitionOwners names a node that no longer carries a transition rule")
		})
	})

	// The rules below run through the API server with JSON patches that remove or replace the field a rule
	// guards. The CRD schema refuses each removal because the node is required, so the rule is never skipped.
	Describe("JSON patches through the API server", func() {
		patch := func(obj client.Object, body string) error {
			return k8sClient.Patch(context.Background(), obj, client.RawPatch(types.JSONPatchType, []byte(body)))
		}

		Describe("TalosEtcdBackup", func() {
			var obj *TalosEtcdBackup

			BeforeEach(func() {
				obj = &TalosEtcdBackup{
					GenerateName: literalGenerateName,
					Namespace:    testNamespace,
					Spec: TalosEtcdBackupSpec{
						ClusterRef: corev1.LocalObjectReference{Name: literalCluster},
						Destination: TalosEtcdBackupDestination{S3: TalosEtcdBackupS3{
							Endpoint:             "https://s3.example.com",
							Bucket:               "backups",
							Prefix:               "etcd",
							CredentialsSecretRef: corev1.LocalObjectReference{Name: literalSecrets},
						}},
					},
				}
				Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
			})

			AfterEach(func() { Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed()) })

			It("refuses to remove spec.clusterRef", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec/clusterRef"}]`)).To(MatchError(ContainSubstring("spec.clusterRef: Required value")))
			})

			It("refuses to remove the name of spec.clusterRef", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec/clusterRef/name"}]`)).To(MatchError(ContainSubstring("clusterRef cannot change after creation")))
			})

			It("refuses to change spec.clusterRef", func() {
				Expect(patch(obj, `[{"op":"replace","path":"/spec/clusterRef","value":{"name":"other"}}]`)).To(MatchError(ContainSubstring("clusterRef cannot change after creation")))
			})

			It("refuses to remove spec", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec"}]`)).To(MatchError(ContainSubstring("spec: Required value")))
			})
		})

		Describe("TalosOperation", func() {
			var obj *TalosOperation

			BeforeEach(func() {
				obj = &TalosOperation{
					GenerateName: literalGenerateName,
					Namespace:    testNamespace,
					Spec: TalosOperationSpec{
						ClusterRef: corev1.LocalObjectReference{Name: literalCluster},
						Kind:       TalosOperationKindTalos,
					},
				}
				Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
			})

			AfterEach(func() { Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed()) })

			It("refuses to remove spec.kind", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec/kind"}]`)).To(MatchError(ContainSubstring("spec.kind: Required value")))
			})

			It("refuses to remove spec.clusterRef", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec/clusterRef"}]`)).To(MatchError(ContainSubstring("spec.clusterRef: Required value")))
			})

			It("refuses to change spec.kind", func() {
				Expect(patch(obj, `[{"op":"replace","path":"/spec/kind","value":"kubernetes"}]`)).To(MatchError(ContainSubstring("kind cannot change after creation")))
			})

			It("refuses to remove spec", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec"}]`)).To(MatchError(ContainSubstring("spec: Required value")))
			})
		})

		Describe("TalosCredentialRequest", func() {
			var obj *TalosCredentialRequest

			BeforeEach(func() {
				obj = &TalosCredentialRequest{
					GenerateName: literalGenerateName,
					Namespace:    testNamespace,
					Spec: TalosCredentialRequestSpec{
						ClusterRef: corev1.LocalObjectReference{Name: literalCluster},
						Purpose:    CredentialPurposePublic,
					},
				}
				Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
			})

			AfterEach(func() { Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed()) })

			It("refuses to remove spec", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec"}]`)).To(MatchError(ContainSubstring("spec: Required value")))
			})

			It("refuses to remove spec.clusterRef", func() {
				Expect(patch(obj, `[{"op":"remove","path":"/spec/clusterRef"}]`)).To(MatchError(ContainSubstring("spec.clusterRef: Required value")))
			})

			It("refuses to change spec.purpose", func() {
				Expect(patch(obj, `[{"op":"replace","path":"/spec/purpose","value":"node-reader"}]`)).To(MatchError(ContainSubstring("a credential request cannot change")))
			})
		})

		Describe("TalosCluster spec.cni", func() {
			It("refuses to replace spec.cni with {} on a cluster that replaces kube-proxy, without the confirm-kube-proxy-switch annotation", func() {
				obj := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyReplaced})
				Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
				DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed()) })

				Expect(patch(obj, `[{"op":"replace","path":"/spec/cni","value":{}}]`)).To(MatchError(ContainSubstring("requires the ichor.maesh.dev/confirm-kube-proxy-switch annotation")))
			})

			It("admits a cni replaced by {} without changing the preset, because the default restores it", func() {
				obj := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium})
				Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
				DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed()) })

				Expect(patch(obj, `[{"op":"replace","path":"/spec/cni","value":{}}]`)).To(Succeed())

				readBack := &TalosCluster{}
				Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, readBack)).To(Succeed())
				Expect(readBack.Spec.CNI.Preset).To(Equal(cniPresetCilium))
			})
		})
	})
})
