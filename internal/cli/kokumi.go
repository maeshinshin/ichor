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
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// kokumiVersion is the Kokumi release this build knows: the
// install.yaml asset of github.com/kokumi-dev/kokumi release 0.17.0
// (2026-10-11, sha256 345223726800ca3f08f633c76c91a38265bc0c131551b24dc49c69ee0cd7b840).
// Ichor never installs it; the version only keys the image list.
const kokumiVersion = "0.17.0"

const (
	// kokumiRepoCredsSecretName is fixed so a re-run is idempotent.
	kokumiRepoCredsSecretName = "kokumi-registry-repo-creds"
	kokumiRepoCredsType       = "repo-creds"
	// kokumiRegistryURL is the bundled registry's Service in the install
	// manifest (kokumi/kokumi-registry, port 5000, plain HTTP).
	kokumiForceHTTP   = "true"
	kokumiRegistryURL = "oci://kokumi-registry.kokumi.svc.cluster.local:5000"
)

// kokumiImages are the images Kokumi's 0.17.0 install.yaml runs: the operator
// (one image serves both the controller and the server Deployments) and the
// bundled registry.
func kokumiImages() []string {
	return []string{
		"ghcr.io/kokumi-dev/kokumi:" + kokumiVersion,
		"docker.io/library/registry:3.1.2@sha256:ddf754342cfc8acc51a56d5d0ab6af06826461864460636d8bd5c546dab2a7b8",
	}
}

// buildKokumiRepoCredsSecret is the Argo CD repo-creds Secret that lets Argo CD
// pull Kokumi's OCI artifacts. It holds no credential; it is still
// created from the CLI, never written to Git.
func buildKokumiRepoCredsSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		Name:      kokumiRepoCredsSecretName,
		Namespace: namespace,
		Labels:    map[string]string{repositorySecretTypeLabel: kokumiRepoCredsType},
		StringData: map[string]string{
			"type":                 "oci",
			"url":                  kokumiRegistryURL,
			"insecureOCIForceHttp": kokumiForceHTTP,
		},
	}
}

func validateKokumiOptions(opts initOptions) error {
	if opts.Kokumi && !opts.ArgoCD {
		return fmt.Errorf("init: --kokumi needs --argocd: Kokumi sits in front of Argo CD")
	}

	return nil
}
