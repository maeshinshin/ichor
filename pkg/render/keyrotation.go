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

package render

import (
	"errors"
	"fmt"
	"slices"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// KeyRotation is the system-layer input of a service-account and secretbox key rotation in progress: the stage the
// control planes are being taken to and the new keys. The bundle Render is given
// stays the one the nodes were built from (the old keys); the stage decides which key signs or encrypts and
// which are accepted besides it.
//
//	Accepting: old key signs/encrypts; the new service-account public key verifies and the new secretbox key
//	           decrypts (a second provider).
//	Issuing, Reencrypting, SettlingTokens: the new key signs/encrypts (first provider); the old one is accepted.
//	Dropping, Finalizing: the new key alone -- byte for byte the configuration a bundle that already holds the
//	           new keys renders, which keeps the machine controller from seeing drift when the rotation ends.
//
// Only the rotation executor sets it. A worker carries neither document, so it renders unchanged.
type KeyRotation struct {
	Stage string
	New   *secretsbundle.KeyRotation
}

// inject edits the service-account and etcd encryption documents of the control-plane configuration cfg that
// was generated from bundle. It is the identity for a nil r and for a worker.
func (r *KeyRotation) inject(cfg coreconfig.Provider, bundle *secrets.Bundle, machineType machine.Type) (coreconfig.Provider, error) {
	if r == nil {
		return cfg, nil
	}

	if r.New == nil {
		return nil, errors.New("render: key rotation has no new keys")
	}

	switch r.Stage {
	case ichorv1alpha1.KeyRotationStageAccepting, ichorv1alpha1.KeyRotationStageIssuing, ichorv1alpha1.KeyRotationStageReencrypting,
		ichorv1alpha1.KeyRotationStageSettlingTokens, ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing:
	default:
		return nil, fmt.Errorf("render: key rotation stage %q is not one a configuration is rendered for", r.Stage)
	}

	if !machineType.IsControlPlane() {
		return cfg, nil
	}

	var err error

	if r.New.ServiceAccount != nil {
		if cfg, err = r.injectServiceAccount(cfg, bundle); err != nil {
			return nil, err
		}
	}

	if r.New.Secretbox != "" {
		if cfg, err = r.injectSecretbox(cfg, bundle); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// requireDocument fails with the way out when cfg does not carry the multi-document form of a Kubernetes
// setting. The legacy contract keeps one service-account key and one secretbox secret in single fields, which
// cannot hold two keys.
func requireDocument(cfg coreconfig.Provider, kind string) error {
	if cfg.Has(kind) {
		return nil
	}

	return fmt.Errorf("render: the configuration has no %s document, so it cannot hold two keys at once; "+
		"a key rotation needs versionContract v1.14 or later", kind)
}

func (r *KeyRotation) injectServiceAccount(cfg coreconfig.Provider, bundle *secrets.Bundle) (coreconfig.Provider, error) {
	if err := requireDocument(cfg, k8s.KubeServiceAccountConfig); err != nil {
		return nil, err
	}

	oldKey, err := bundle.Certs.K8sServiceAccount.GetKey()
	if err != nil {
		return nil, errors.New("render: the bundle's service-account key is not a PEM key")
	}

	newKey, err := r.New.ServiceAccount.GetKey()
	if err != nil {
		return nil, errors.New("render: the new service-account key is not a PEM key")
	}

	oldPub, newPub := string(oldKey.GetPublicKeyPEM()), string(newKey.GetPublicKeyPEM())

	return container.PatchDocument(cfg, func(doc *k8s.KubeServiceAccountConfigV1Alpha1) error {
		switch r.Stage {
		case ichorv1alpha1.KeyRotationStageAccepting:
			doc.ServiceAccepted.PublicKeys = append(slices.Clone(doc.ServiceAccepted.PublicKeys), newPub)
		case ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing:
			doc.ServiceIssuer.PrivateKey = string(r.New.ServiceAccount.Key)
		default: // Issuing, Reencrypting, SettlingTokens
			doc.ServiceIssuer.PrivateKey = string(r.New.ServiceAccount.Key)
			doc.ServiceAccepted.PublicKeys = append(slices.Clone(doc.ServiceAccepted.PublicKeys), oldPub)
		}

		return nil
	})
}

func (r *KeyRotation) injectSecretbox(cfg coreconfig.Provider, bundle *secrets.Bundle) (coreconfig.Provider, error) {
	if err := requireDocument(cfg, k8s.KubeEtcdEncryptionConfig); err != nil {
		return nil, err
	}

	old := bundle.Secrets.SecretboxEncryptionSecret
	if old == "" {
		return nil, errors.New("render: the secrets bundle has no secretbox secret to rotate")
	}

	return container.PatchDocument(cfg, func(doc *k8s.KubeEtcdEncryptionConfigV1Alpha1) error {
		return r.rotateProviders(doc.Config.Object, old)
	})
}

// rotateProviders rewrites the providers list of the first resources entry of an EncryptionConfiguration.
// The new key is its own provider next to the one that holds the old secret, not a second key in it: the
// Kubernetes source names that shape as the supported overlap, and the new key keeps the old key's name
// because that name is the prefix of what it writes, and the finished configuration names it so.
func (r *KeyRotation) rotateProviders(obj map[string]any, old string) error {
	resources, _ := obj["resources"].([]any)
	if len(resources) == 0 {
		return errors.New("render: the etcd encryption configuration has no resources entry")
	}

	entry, _ := resources[0].(map[string]any)

	providers, _ := entry["providers"].([]any)

	at := slices.IndexFunc(providers, func(p any) bool { return secretboxKeyName(p, old) != "" })
	if at < 0 {
		return errors.New("render: the etcd encryption configuration has no secretbox provider holding the bundle's secretbox secret")
	}

	name := secretboxKeyName(providers[at], old)

	fresh := map[string]any{"secretbox": map[string]any{"keys": []any{map[string]any{"name": name, "secret": r.New.Secretbox}}}}

	switch r.Stage {
	case ichorv1alpha1.KeyRotationStageAccepting:
		providers = slices.Insert(providers, at+1, any(fresh))
	case ichorv1alpha1.KeyRotationStageDropping, ichorv1alpha1.KeyRotationStageFinalizing:
		providers[at] = fresh
	default: // Issuing, Reencrypting, SettlingTokens
		providers = slices.Insert(providers, at, any(fresh))
	}

	entry["providers"] = providers

	return nil
}

// secretboxKeyName reports the name of the key of provider p whose secret is secret, "" when p is not a
// secretbox provider holding it.
func secretboxKeyName(p any, secret string) string {
	provider, _ := p.(map[string]any)
	box, _ := provider["secretbox"].(map[string]any)
	keys, _ := box["keys"].([]any)

	for _, k := range keys {
		key, _ := k.(map[string]any)
		if key["secret"] == secret {
			name, _ := key["name"].(string)

			return name
		}
	}

	return ""
}
