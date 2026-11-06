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
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	actionsAdminUser    = "act-admin"
	actionsOperatorUser = "act-operator"
	actionsViewerUser   = "act-viewer"
)

// The UI hides what the caller cannot do: GetMyPermissions answers the action flags with one
// SelfSubjectAccessReview each, as the built-in roles grant them (config/rbac/ichor_*_role.yaml).
var _ = Describe("Action permissions", func() {
	BeforeEach(func() {
		group := ichorv1alpha1.GroupVersion.Group
		rule := func(resources []string, verbs ...string) rbacv1.PolicyRule {
			return rbacv1.PolicyRule{APIGroups: []string{group}, Resources: resources, Verbs: verbs}
		}
		read := rule([]string{resourceTalosClusters, resourceTalosOperations, "talosetcdbackups"}, "get", "list", "watch")
		operator := []rbacv1.PolicyRule{read, rule([]string{resourceTalosOperations}, "create"), rule([]string{"talosetcdbackups"}, "update")}
		admin := append([]rbacv1.PolicyRule{
			rule([]string{"ichorusers"}, "list"),
			rule([]string{"talosoperations/ca-rotation", "talosoperations/key-rotation", "talosoperations/shutdown", "talosoperations/etcd-restore"}, "start"),
			rule([]string{"ichorinstallations/version"}, "update"),
		}, operator...)

		for user, rules := range map[string][]rbacv1.PolicyRule{actionsAdminUser: admin, actionsOperatorUser: operator, actionsViewerUser: {read}} {
			name := "actions-test-" + user
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: name, Rules: rules}))).To(Succeed())
			Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
				Name:     name,
				RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: reflect.TypeFor[rbacv1.ClusterRole]().Name(), Name: name},
				Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: user}},
			}))).To(Succeed())
		}
	})

	It("reports what each role may start, change and list", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		type flags struct{ users, create, ca, key, shutdown, restore, version, backups bool }

		for user, want := range map[string]flags{
			actionsAdminUser:    {true, true, true, true, true, true, true, true},
			actionsOperatorUser: {false, true, false, false, false, false, false, true},
			actionsViewerUser:   {},
		} {
			resp, err := d.GetMyPermissions(ctx, asDiagUser(user, &pb.GetMyPermissionsRequest{Cluster: objectRef(testNamespace, "any-cluster")}))
			Expect(err).NotTo(HaveOccurred(), user)

			m := resp.Msg
			Expect(flags{
				m.GetListUsers(), m.GetCreateOperations(), m.GetStartCaRotation(), m.GetStartKeyRotation(), m.GetStartShutdown(),
				m.GetStartEtcdRestore(), m.GetUpdateIchorVersion(), m.GetUpdateBackups(),
			}).To(Equal(want), user)
		}
	})

	It("answers whether the caller may list users without a cluster", func() {
		d, _, closeFn := newDiagnosticsServer(&fakeDownloadClient{}, k8sClient, nil)
		defer closeFn()

		for user, want := range map[string]bool{actionsAdminUser: true, actionsOperatorUser: false, actionsViewerUser: false} {
			resp, err := d.GetMyPermissions(ctx, asDiagUser(user, &pb.GetMyPermissionsRequest{}))
			Expect(err).NotTo(HaveOccurred(), user)
			Expect(resp.Msg.GetListUsers()).To(Equal(want), user)
			Expect(resp.Msg.GetCreateOperations()).To(BeFalse(), user)
		}
	})
})
