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
	"net/url"
	"slices"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	resblock "github.com/siderolabs/talos/pkg/machinery/resources/block"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ValidateSecurity checks spec.security as a whole (ADR 0108): the shapes the CRD cannot say and
// the version contract each option needs. The admission webhook and Render both call it, so a
// cluster the webhook let in is a cluster that renders.
func ValidateSecurity(c *ichorv1alpha1.TalosCluster) error {
	s := c.Spec.Security
	if s == nil {
		return nil
	}

	contract, err := coreconfig.ParseContractFromVersion(c.Spec.VersionContract)
	if err != nil {
		return fmt.Errorf("spec.versionContract: %w", err)
	}

	var errs []error

	if FirewallEnabled(c) {
		// Planning for a control plane covers every rule of a worker too.
		if _, err := firewallPlan(c, machine.TypeControlPlane); err != nil {
			errs = append(errs, err)
		}
	}

	if s.PodSecurity != nil && !contract.MultidocKubernetesConfigSupported() {
		errs = append(errs, errors.New("spec.security.podSecurity needs versionContract v1.14 or later"))
	}

	if s.Audit != nil && !contract.MultidocKubernetesConfigSupported() {
		errs = append(errs, errors.New("spec.security.audit needs versionContract v1.14 or later"))
	}

	if d := s.DiskEncryption; d != nil {
		if !contract.VolumeConfigEncryptionSupported() {
			errs = append(errs, errors.New("spec.security.diskEncryption needs a versionContract "+
				"that supports VolumeConfig encryption (v1.11 or later)"))
		}

		if d.Type == ichorv1alpha1.DiskEncryptionKMS {
			if u, err := url.Parse(d.KMSEndpoint); err != nil || u.Scheme != "https" || u.Host == "" {
				errs = append(errs, fmt.Errorf("spec.security.diskEncryption.kmsEndpoint %q must be an https:// URL: "+
					"any other scheme sends the key material without TLS", d.KMSEndpoint))
			}
		}
	}

	return errors.Join(errs...)
}

// injectPodSecurity renders spec.security.podSecurity: the defaults and exemptions of the
// PodSecurity plugin, merged over Talos's own document so every field left unset keeps its default.
func injectPodSecurity(
	cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster, machineType machine.Type,
) (coreconfig.Provider, error) {
	ps := securityOf(cluster).PodSecurity
	if ps == nil || machineType != machine.TypeControlPlane {
		return cfg, nil
	}

	defaults := map[string]any{}

	for k, v := range map[string]string{"enforce": ps.Enforce, "audit": ps.Audit, "warn": ps.Warn} {
		if v != "" {
			defaults[k] = v
			defaults[k+"-version"] = "latest"
		}
	}

	obj := map[string]any{
		"apiVersion": "pod-security.admission.config.k8s.io/v1alpha1",
		"kind":       "PodSecurityConfiguration",
	}

	if len(defaults) > 0 {
		obj["defaults"] = defaults
	}

	if len(ps.ExemptNamespaces) > 0 {
		ns := make([]any, 0, len(ps.ExemptNamespaces))
		for _, n := range slices.Sorted(slices.Values(ps.ExemptNamespaces)) {
			ns = append(ns, n)
		}

		obj["exemptions"] = map[string]any{"namespaces": ns}
	}

	doc := k8s.NewKubeAdmissionControlConfigV1Alpha1()
	doc.MetaName = "PodSecurity"
	doc.PluginConfig.Object = obj

	return mergeDocument(cfg, "pod security admission", doc)
}

// securityOf returns spec.security, never nil.
func securityOf(c *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosClusterSecurity {
	if c.Spec.Security == nil {
		return &ichorv1alpha1.TalosClusterSecurity{}
	}

	return c.Spec.Security
}

// auditRule is one rule of an audit policy.
func auditRule(level string, verbs []any, resources ...any) map[string]any {
	r := map[string]any{"level": level}
	if verbs != nil {
		r["verbs"] = verbs
	}

	if resources != nil {
		r["resources"] = resources
	}

	return r
}

// auditGroup selects resources of an API group; no names selects the whole group.
func auditGroup(group string, names ...any) map[string]any {
	g := map[string]any{"group": group}
	if names != nil {
		g["resources"] = names
	}

	return g
}

// auditRules are the rules of each audit preset. The first matching rule wins, so the
// never-above-Metadata rule for credentials-bearing resources comes first.
func auditRules(policy string) ([]any, error) {
	quiet := auditRule("Metadata", nil,
		auditGroup("", "secrets", "configmaps", "serviceaccounts/token"),
		auditGroup("authentication.k8s.io", "tokenreviews"))
	catchAll := auditRule("Metadata", nil)
	writes := []any{"create", "update", "patch", "delete", "deletecollection"}

	switch policy {
	case ichorv1alpha1.AuditPolicyMetadata:
		return []any{catchAll}, nil
	case ichorv1alpha1.AuditPolicyRBACChange:
		return []any{quiet, auditRule("RequestResponse", writes, auditGroup("rbac.authorization.k8s.io")), catchAll}, nil
	case ichorv1alpha1.AuditPolicyWrites:
		return []any{quiet, auditRule("Request", writes), catchAll}, nil
	default:
		return nil, fmt.Errorf("render: unknown audit policy %q", policy)
	}
}

// injectAudit renders spec.security.audit as the KubeAuditPolicyConfig document.
func injectAudit(
	cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster, machineType machine.Type,
) (coreconfig.Provider, error) {
	a := securityOf(cluster).Audit
	if a == nil || machineType != machine.TypeControlPlane {
		return cfg, nil
	}

	rules, err := auditRules(a.Policy)
	if err != nil {
		return nil, err
	}

	policy := map[string]any{"apiVersion": "audit.k8s.io/v1", "kind": "Policy", "rules": rules}
	if a.Policy != ichorv1alpha1.AuditPolicyMetadata {
		policy["omitStages"] = []any{"RequestReceived"}
	}

	doc := k8s.NewKubeAuditPolicyConfigV1Alpha1()
	doc.AuditConfig.Object = policy

	return mergeDocument(cfg, "audit policy", doc)
}

// injectDiskEncryption renders spec.security.diskEncryption as VolumeConfig documents for STATE
// and EPHEMERAL, on every node. EPHEMERAL's key is also locked to STATE (the Talos disk
// encryption guide recommends it for volumes other than STATE).
func injectDiskEncryption(
	cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster,
) (coreconfig.Provider, error) {
	d := securityOf(cluster).DiskEncryption
	if d == nil {
		return cfg, nil
	}

	docs := make([]config.Document, 0, 2)

	for _, name := range []string{constants.StatePartitionLabel, constants.EphemeralPartitionLabel} {
		key := block.EncryptionKey{KeySlot: 0}

		switch d.Type {
		case ichorv1alpha1.DiskEncryptionTPM:
			key.KeyTPM = &block.EncryptionKeyTPM{TPMCheckSecurebootStatusOnEnroll: d.RequireSecureBoot}
		case ichorv1alpha1.DiskEncryptionNodeID:
			key.KeyNodeID = &block.EncryptionKeyNodeID{}
		case ichorv1alpha1.DiskEncryptionKMS:
			key.KeyKMS = &block.EncryptionKeyKMS{KMSEndpoint: d.KMSEndpoint}
		default:
			return nil, fmt.Errorf("render: unknown disk encryption type %q", d.Type)
		}

		if name == constants.EphemeralPartitionLabel {
			key.KeyLockToSTATE = new(true)
		}

		v := block.NewVolumeConfigV1Alpha1()
		v.MetaName = name
		v.EncryptionSpec = block.EncryptionSpec{
			EncryptionProvider: resblock.EncryptionProviderLUKS2,
			EncryptionKeys:     []block.EncryptionKey{key},
		}
		docs = append(docs, v)
	}

	return mergeDocument(cfg, "disk encryption", docs...)
}

// injectSecurity runs the opt-in hardening steps of ADR 0108 in order; each returns cfg
// untouched when its option is unset.
func injectSecurity(
	cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster, machineType machine.Type,
) (coreconfig.Provider, error) {
	if err := ValidateSecurity(cluster); err != nil {
		return nil, fmt.Errorf("render: spec.security: %w", err)
	}

	var err error

	if cfg, err = injectFirewall(cfg, cluster, machineType); err != nil {
		return nil, err
	}

	if cfg, err = injectPodSecurity(cfg, cluster, machineType); err != nil {
		return nil, err
	}

	if cfg, err = injectAudit(cfg, cluster, machineType); err != nil {
		return nil, err
	}

	return injectDiskEncryption(cfg, cluster)
}
