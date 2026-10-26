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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

func gitRepo() *TalosClusterGitRepository {
	return &TalosClusterGitRepository{
		Host: "github", APIURL: "https://api.github.com", Owner: "acme", Repo: "infra",
		CloneURL: "https://github.com/acme/infra.git",
		TokenSecretRef: corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "git-token"}, Key: "token",
		},
	}
}

var _ = Describe("TalosCluster spec.gitops", func() {
	cluster := func(g *TalosClusterGitOps) *TalosCluster {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.GitOps = g

		return c
	}

	It("defaults mode to relaxed and baseBranch to main", func() {
		c := cluster(&TalosClusterGitOps{Repository: gitRepo()})
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
		Expect(c.Spec.GitOps.Mode).To(Equal("relaxed"))
		Expect(c.Spec.GitOps.Repository.BaseBranch).To(Equal("main"))
	})

	It("accepts strict with a repository and machinesPath", func() {
		r := gitRepo()
		r.MachinesPath = "clusters/prod/machines"
		Expect(k8sClient.Create(context.Background(), cluster(&TalosClusterGitOps{Mode: "strict", Repository: r}))).To(Succeed())
	})

	DescribeTable("rejected shapes",
		func(mutate func(*TalosClusterGitOps)) {
			g := &TalosClusterGitOps{Mode: "strict", Repository: gitRepo()}
			g.Repository.MachinesPath = "machines"
			mutate(g)
			Expect(k8sClient.Create(context.Background(), cluster(g))).NotTo(Succeed())
		},
		Entry("strict without a repository", func(g *TalosClusterGitOps) { g.Repository = nil }),
		Entry("strict without machinesPath", func(g *TalosClusterGitOps) { g.Repository.MachinesPath = "" }),
		Entry("machinesPath with ..", func(g *TalosClusterGitOps) { g.Repository.MachinesPath = "a/../b" }),
		Entry("absolute machinesPath", func(g *TalosClusterGitOps) { g.Repository.MachinesPath = "/etc" }),
		Entry("http cloneURL", func(g *TalosClusterGitOps) { g.Repository.CloneURL = "http://github.com/acme/infra.git" }),
		Entry("http apiURL", func(g *TalosClusterGitOps) { g.Repository.APIURL = "http://api.github.com" }),
		Entry("unknown host", func(g *TalosClusterGitOps) { g.Repository.Host = "bitbucket" }),
		Entry("owner with ..", func(g *TalosClusterGitOps) { g.Repository.Owner = "../x" }),
		Entry("unknown mode", func(g *TalosClusterGitOps) { g.Mode = "lax" }),
	)
})
