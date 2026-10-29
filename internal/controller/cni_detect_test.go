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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ds(ns, name, image string) appsv1.DaemonSet {
	return appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
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
		{"none", []appsv1.DaemonSet{ds("kube-system", "kube-proxy", "k8s/kube-proxy:v1.34.0")}, "none", ""},
		{"empty", nil, "none", ""},
		{"cilium", []appsv1.DaemonSet{ds("kube-system", "cilium", "quay.io/cilium/cilium:v1.18.3@sha256:abc"), ds("kube-system", "kube-proxy", "x:1")}, "cilium", "v1.18.3"},
		{"flannel", []appsv1.DaemonSet{ds("kube-system", "kube-flannel", "ghcr.io/siderolabs/flannel:v0.27.4")}, "flannel", "v0.27.4"},
		{"calico", []appsv1.DaemonSet{ds("kube-system", "calico-node", "calico/node:v3.30.0")}, "calico", "v3.30.0"},
		{"other", []appsv1.DaemonSet{ds("kube-system", "weave-net", "weave:2.8.1")}, "other", "2.8.1"},
		{"two CNIs", []appsv1.DaemonSet{ds("kube-system", "cilium", "c:v1"), ds("kube-system", "kube-flannel-ds", "f:v2")}, "other", ""},
		{"other namespace ignored", []appsv1.DaemonSet{ds("default", "cilium", "c:v1")}, "none", ""},
		{"registry port, no tag", []appsv1.DaemonSet{ds("kube-system", "cilium", "reg:5000/cilium")}, "cilium", ""},
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
