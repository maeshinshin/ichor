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
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	authnv1 "k8s.io/api/authentication/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The role-grant and last-admin rules of the UI backend (internal/uiapi guardGrant and
// guardLastAdmin) hold for a hand-written CR too.

const userNS = "ichor-system"

func iuser(name string, disabled bool, roles ...string) *ichorv1alpha1.IchorUser {
	return &ichorv1alpha1.IchorUser{
		ObjectMeta: metav1.ObjectMeta{Namespace: userNS, Name: name},
		Spec:       ichorv1alpha1.IchorUserSpec{Roles: roles, Disabled: disabled},
	}
}

func userRequestCtx(username string, groups ...string) context.Context {
	var req admission.Request

	req.UserInfo = authnv1.UserInfo{Username: username, Groups: groups}

	return admission.NewContextWithRequest(context.Background(), req)
}

func userValidator(t *testing.T, stored ...*ichorv1alpha1.IchorUser) *IchorUserCustomValidator {
	t.Helper()

	b := fake.NewClientBuilder().WithScheme(schemeOf(t))
	for _, u := range stored {
		b = b.WithObjects(u.DeepCopy())
	}

	return &IchorUserCustomValidator{Reader: b.Build(), ExemptUsers: []string{testControllerUser, "system:serviceaccount:kube-system:namespace-controller"}}
}

func wantRefusal(t *testing.T, err error, want string) {
	t.Helper()

	switch {
	case want == "" && err != nil:
		t.Fatalf("got %v, want it admitted", err)
	case want != "" && err == nil:
		t.Fatalf("admitted, want a refusal containing %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Fatalf("got %q, want it to contain %q", err, want)
	}
}

func TestIchorUserGrant(t *testing.T) {
	t.Parallel()

	alice := []string{"ichor:operator"}
	boss := []string{"ichor:admin"}

	for _, tc := range []struct {
		name     string
		user     string
		groups   []string
		old, new *ichorv1alpha1.IchorUser // old nil: a create
		want     string
	}{
		{name: "an operator creates an operator", user: "ichor:alice", groups: alice, new: iuser("bob", false, "operator")},
		{name: "an operator cannot create an admin", user: "ichor:alice", groups: alice, new: iuser("bob", false, "admin"), want: `role "admin"`},
		{name: "an operator cannot create a role it lacks", user: "ichor:alice", groups: alice, new: iuser("bob", false, "operator", "viewer"), want: `role "viewer"`},
		{name: "an admin creates any role", user: "ichor:boss", groups: boss, new: iuser("bob", false, "admin", "custom")},
		{name: "an operator cannot make an admin of a viewer", user: "ichor:alice", groups: alice,
			old: iuser("bob", false, "viewer"), new: iuser("bob", false, "viewer", "admin"), want: `role "admin"`},
		{name: "an operator may edit an admin without adding a role", user: "ichor:alice", groups: alice,
			old: iuser("bob", false, "admin"), new: iuser("bob", false, "admin")},
		{name: "an operator may drop a role", user: "ichor:alice", groups: alice,
			old: iuser("bob", false, "viewer", "operator"), new: iuser("bob", false, "operator")},
		{name: "an external identity is gated by RBAC alone", user: "oidc:carol", groups: []string{"oidc:team"}, new: iuser("bob", false, "admin")},
		{name: "a person with a certificate is gated by RBAC alone", user: "dave", new: iuser("bob", false, "admin")},
		{name: "the controller is not limited", user: testControllerUser, new: iuser("bob", false, "admin")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The last-admin rule has no say here: an admin always remains.
			v := userValidator(t, iuser("root", false, "admin"))
			ctx := userRequestCtx(tc.user, tc.groups...)

			var err error
			if tc.old == nil {
				_, err = v.ValidateCreate(ctx, tc.new)
			} else {
				_, err = v.ValidateUpdate(ctx, tc.old, tc.new)
			}

			wantRefusal(t, err, tc.want)
		})
	}
}

func TestIchorUserLastAdmin(t *testing.T) {
	t.Parallel()

	const lastAdmin = "the last enabled admin"

	root := iuser("root", false, "admin")
	other := iuser("other", false, "admin")
	now := metav1.Now()

	terminating := func(disabled bool) *ichorv1alpha1.IchorUser {
		u := iuser("root", disabled, "admin")
		u.DeletionTimestamp, u.Finalizers = &now, []string{"x"}

		return u
	}

	boss := []string{"ichor:admin"}

	type update struct{ old, new *ichorv1alpha1.IchorUser }

	for _, tc := range []struct {
		name   string
		user   string
		groups []string
		stored []*ichorv1alpha1.IchorUser
		del    *ichorv1alpha1.IchorUser
		upd    *update
		want   string
	}{
		{name: "deleting the only admin", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root}, del: root, want: lastAdmin},
		{name: "deleting an admin when another is enabled", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root, other}, del: root},
		{name: "deleting an admin when the other is disabled", user: "ichor:boss", groups: boss,
			stored: []*ichorv1alpha1.IchorUser{root, iuser("other", true, "admin")}, del: root, want: lastAdmin},
		{name: "deleting an admin when the other is no admin", user: "ichor:boss", groups: boss,
			stored: []*ichorv1alpha1.IchorUser{root, iuser("other", false, "operator")}, del: root, want: lastAdmin},
		{name: "deleting a user who is no admin", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root, iuser("op", false, "operator")},
			del: iuser("op", false, "operator")},
		{name: "deleting a disabled admin", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root, iuser("off", true, "admin")},
			del: iuser("off", true, "admin")},
		{name: "an external admin is limited too", user: "oidc:carol", groups: []string{"oidc:ops"}, stored: []*ichorv1alpha1.IchorUser{root}, del: root, want: lastAdmin},
		{name: "disabling the only admin", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root},
			upd: &update{root, iuser("root", true, "admin")}, want: lastAdmin},
		{name: "demoting the only admin", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root},
			upd: &update{root, iuser("root", false, "operator")}, want: lastAdmin},
		{name: "demoting an admin when another is enabled", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root, other},
			upd: &update{root, iuser("root", false, "operator")}},
		{name: "editing the only admin without demoting it", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root},
			upd: &update{root, iuser("root", false, "admin", "viewer")}},
		{name: "enabling a disabled admin", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{root},
			upd: &update{iuser("off", true, "admin"), iuser("off", false, "admin")}},
		{name: "the override certificate may remove the last admin", user: "admin", groups: []string{"system:masters"},
			stored: []*ichorv1alpha1.IchorUser{root}, del: root},
		{name: "the override certificate may demote the last admin", user: "admin", groups: []string{"system:masters"},
			stored: []*ichorv1alpha1.IchorUser{root}, upd: &update{root, iuser("root", true, "admin")}},
		{name: "the namespace controller deletes everything", user: "system:serviceaccount:kube-system:namespace-controller",
			stored: []*ichorv1alpha1.IchorUser{root}, del: root},
		{name: "an object already being deleted is not guarded", user: "ichor:boss", groups: boss, stored: []*ichorv1alpha1.IchorUser{terminating(false)},
			upd: &update{terminating(false), terminating(true)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := userValidator(t, tc.stored...)
			ctx := userRequestCtx(tc.user, tc.groups...)

			var err error
			if tc.del != nil {
				_, err = v.ValidateDelete(ctx, tc.del)
			} else {
				_, err = v.ValidateUpdate(ctx, tc.upd.old, tc.upd.new)
			}

			wantRefusal(t, err, tc.want)
		})
	}
}

func TestIchorUserWithoutARequest(t *testing.T) {
	t.Parallel()

	v := userValidator(t)

	_, err := v.ValidateCreate(context.Background(), iuser("bob", false, "admin"))
	wantRefusal(t, err, "no admission request")
}


// Through the API server and its real RBAC, as users of Ichor's own issuer: the apiserver names them ichor:<name>
// and puts them in the groups ichor:<role>.
var _ = Describe("IchorUser Webhook through the API server", func() {
	const alice, boss = "ichor:alice", "ichor:boss"

	as := func(user string, groups ...string) client.Client {
		c := rest.CopyConfig(cfg)
		c.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}

		cl, err := client.New(c, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		return cl
	}

	user := func(name string, roles ...string) *ichorv1alpha1.IchorUser {
		return &ichorv1alpha1.IchorUser{
			ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name},
			Spec:       ichorv1alpha1.IchorUserSpec{Roles: roles},
		}
	}

	BeforeEach(func() {
		all := rbacv1.PolicyRule{APIGroups: []string{ichorGroup}, Resources: []string{"ichorusers"}, Verbs: []string{"*"}}
		bindRole("ichoruser-gate-alice", alice, all)
		bindRole("ichoruser-gate-boss", boss, all)
	})

	It("refuses a grant above the requester's own and admits one within it", func() {
		err := as(alice, "ichor:operator").Create(ctx, user("grant-admin", "admin"))
		Expect(err).To(MatchError(ContainSubstring(`you cannot grant the role "admin"`)))

		ok := user("grant-operator", "operator")
		Expect(as(alice, "ichor:operator").Create(ctx, ok)).To(Succeed())
		Expect(k8sClient.Delete(ctx, ok)).To(Succeed())

		anything := user("grant-anything", "admin", "custom")
		Expect(as(boss, "ichor:admin").Create(ctx, anything)).To(Succeed())
		Expect(k8sClient.Delete(ctx, anything)).To(Succeed())
	})

	It("keeps the last enabled admin from a user of Ichor, and not from the override certificate", func() {
		root := user("last-admin", "admin")
		Expect(k8sClient.Create(ctx, root)).To(Succeed())

		asBoss := as(boss, "ichor:admin")

		Expect(asBoss.Delete(ctx, root.DeepCopy())).To(MatchError(ContainSubstring("last enabled admin")))

		disabled := root.DeepCopy()
		disabled.Spec.Disabled = true
		Expect(asBoss.Update(ctx, disabled)).To(MatchError(ContainSubstring("last enabled admin")))

		// With a second enabled admin the first can go.
		second := user("second-admin", "admin")
		Expect(k8sClient.Create(ctx, second)).To(Succeed())
		Expect(asBoss.Delete(ctx, root.DeepCopy())).To(Succeed())

		// The override certificate (system:masters) may remove the last one.
		Expect(k8sClient.Delete(ctx, second)).To(Succeed())
	})
})
