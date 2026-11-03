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

package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

func ds(ns, name, image string) appsv1.DaemonSet {
	return appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, //nolint:modernize
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: image}}},
		}},
	}
}

func TestDetectCNI(t *testing.T) {
	cases := []struct {
		name            string
		sets            []appsv1.DaemonSet
		detected, wantV string
	}{
		{cniNone, []appsv1.DaemonSet{ds("kube-system", "kube-proxy", "k8s/kube-proxy:v1.34.0")}, cniNone, ""},
		{"empty", nil, cniNone, ""},
		{ciliumName, []appsv1.DaemonSet{ds("kube-system", "cilium", "quay.io/cilium/cilium:v1.18.3@sha256:abc"), ds("kube-system", "kube-proxy", "x:1")}, ciliumName, "v1.18.3"},
		{cniFlannel, []appsv1.DaemonSet{ds("kube-system", "kube-flannel", "ghcr.io/siderolabs/flannel:v0.27.4")}, cniFlannel, "v0.27.4"},
		{cniCalico, []appsv1.DaemonSet{ds("kube-system", "calico-node", "calico/node:v3.30.0")}, cniCalico, "v3.30.0"},
		{cniOther, []appsv1.DaemonSet{ds("kube-system", "weave-net", "weave:2.8.1")}, cniOther, "2.8.1"},
		{"two CNIs", []appsv1.DaemonSet{ds("kube-system", "cilium", "c:v1"), ds("kube-system", "kube-flannel-ds", "f:v2")}, cniOther, ""},
		{"other namespace ignored", []appsv1.DaemonSet{ds("default", "cilium", "c:v1")}, cniNone, ""},
		{"registry port, no tag", []appsv1.DaemonSet{ds("kube-system", "cilium", "reg:5000/cilium")}, ciliumName, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, v := DetectCNI(tc.sets)
			if got != tc.detected || v != tc.wantV {
				t.Fatalf("DetectCNI = %q, %q; want %q, %q", got, v, tc.detected, tc.wantV)
			}
		})
	}
}

func TestCNIDetectRefreshesWhenAHandoverOrACNIUpgradeCompletes(t *testing.T) {
	handover := func(status metav1.ConditionStatus) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{Type: render.ConditionCNIHandover, Status: status, Reason: "Test"})

		return c
	}
	op := func(kind, phase string) *ichorv1alpha1.TalosOperation {
		return &ichorv1alpha1.TalosOperation{
			Spec:   ichorv1alpha1.TalosOperationSpec{Kind: kind},
			Status: ichorv1alpha1.TalosOperationStatus{Phase: phase},
		}
	}

	cases := []struct {
		name     string
		old, new client.Object
		want     bool
	}{
		{"a cni operation completes", op(ichorv1alpha1.TalosOperationKindCNI, ichorv1alpha1.TalosOperationPhaseInProgress), op(ichorv1alpha1.TalosOperationKindCNI, ichorv1alpha1.TalosOperationPhaseCompleted), true},
		{"a cni operation is still running", op(ichorv1alpha1.TalosOperationKindCNI, ichorv1alpha1.TalosOperationPhasePending), op(ichorv1alpha1.TalosOperationKindCNI, ichorv1alpha1.TalosOperationPhaseInProgress), false},
		{"a talos operation completes", op(ichorv1alpha1.TalosOperationKindTalos, ichorv1alpha1.TalosOperationPhaseInProgress), op(ichorv1alpha1.TalosOperationKindTalos, ichorv1alpha1.TalosOperationPhaseCompleted), false},
		{"the cilium hand-over completes", handover(metav1.ConditionFalse), handover(metav1.ConditionTrue), true},
		{"the cilium hand-over was already done", handover(metav1.ConditionTrue), handover(metav1.ConditionTrue), false},
		{"a status write of the detection itself", &ichorv1alpha1.TalosCluster{}, &ichorv1alpha1.TalosCluster{}, false},
	}

	for _, c := range cases {
		if got := cniDetectTrigger(c.old, c.new); got != c.want {
			t.Errorf("%s: cniDetectTrigger = %v, want %v", c.name, got, c.want)
		}
	}
}
