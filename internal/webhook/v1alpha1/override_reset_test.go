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
	rbacv1 "k8s.io/api/rbac/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0085: a node reset needs override on the machine, whichever way the
// TalosOperation is written (the CLI and UI check it too, but a user with
// `create` on talosoperations can write the CR by hand).
var _ = Describe("Override webhook on a node reset", func() {
	const (
		opUser    = "op-user"
		opGlass   = "op-glass-user"
		machineNm = "reset-target"
	)

	grantOps := func(user string, glassOnMachine bool) {
		glass := ""
		if glassOnMachine {
			glass = "talosmachines"
		}

		grantOperationRights(user, glass)
	}

	nodeOp := func(action string) *ichorv1alpha1.TalosOperation {
		return &ichorv1alpha1.TalosOperation{
			GenerateName: "node-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Kind: ichorv1alpha1.TalosOperationKindNode,
				Node: &ichorv1alpha1.TalosOperationNodeSpec{
					MachineRef: corev1.LocalObjectReference{Name: machineNm}, Action: action, Confirm: confirmFor(action),
				},
			},
		}
	}

	BeforeEach(func() {
		grantOps(opUser, false)
		grantOps(opGlass, true)
		grantOps(testControllerUser, false)
	})

	It("refuses a hand-written reset from a user without override on the machine", func() {
		err := asUser(opUser).Create(ctx, nodeOp(ichorv1alpha1.NodeActionReset))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("override"))
	})

	It("admits the reset when the user holds override on that machine", func() {
		Expect(asUser(opGlass).Create(ctx, nodeOp(ichorv1alpha1.NodeActionReset))).To(Succeed())
	})

	It("admits Ichor's own controller", func() {
		Expect(asUser(testControllerUser).Create(ctx, nodeOp(ichorv1alpha1.NodeActionReset))).To(Succeed())
	})

	It("does not ask for override on the other node actions", func() {
		Expect(asUser(opUser).Create(ctx, nodeOp("reboot"))).To(Succeed())
	})

	It("does not ask for override on other kinds of operation", func() {
		op := &ichorv1alpha1.TalosOperation{
			GenerateName: "talos-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Kind: ichorv1alpha1.TalosOperationKindTalos,
			},
		}
		Expect(asUser(opUser).Create(ctx, op)).To(Succeed())
	})
})

// grantOperationRights lets user create and get TalosOperations, and, when glassOn names a resource,
// hold the override verb on it. The webhook asks Kubernetes about the verb, so a real role is bound.
func grantOperationRights(user, glassOn string) {
	rules := []rbacv1.PolicyRule{{
		APIGroups: []string{ichorGroup}, Resources: []string{"talosoperations"}, Verbs: []string{"create", "get"},
	}}
	// These tests are about override; the destructive-start right (ADR 0094) is granted so it is
	// not what stops them. start_gate_test.go covers the right itself.
	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{ichorGroup}, Verbs: []string{ichorv1alpha1.OperationStartVerb},
		Resources: []string{"talosoperations/etcd-restore", "talosoperations/node-reset"},
	})

	if glassOn != "" {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{glassOn}, Verbs: []string{OverrideVerb},
		})
	}

	bindRole("bg-ops-"+user+"-"+glassOn, user, rules...)
}

// confirmFor is the typed confirmation the CRD demands of a reset (the
// machine's name) and nothing for the other actions.
func confirmFor(action string) string {
	if action == ichorv1alpha1.NodeActionReset {
		return "reset-target"
	}

	return ""
}
