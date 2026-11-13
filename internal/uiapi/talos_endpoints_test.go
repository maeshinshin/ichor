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
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	epVIP    = "192.168.0.210"
	epCP     = "192.168.0.201:50000"
	epWorker = "192.168.0.205:50000"
)

func endpointTestMachine(name, role, endpoint string, deleting bool) *ichorv1alpha1.TalosMachine {
	m := &ichorv1alpha1.TalosMachine{
		Name: name, Namespace: testNS,
		Spec: ichorv1alpha1.TalosMachineSpec{ClusterRef: corev1.LocalObjectReference{Name: "c"}, Role: role, Endpoint: endpoint},
	}

	if deleting {
		now := metav1.NewTime(time.Now())
		m.DeletionTimestamp, m.Finalizers = &now, []string{"test/hold"}
	}

	return m
}

// A handed-out talosconfig never names the cluster's own endpoint, whatever its type: a VIP follows etcd health and
// is the first thing gone when no control plane is up, and a load balancer or DNS name fronts the Kubernetes API.
// With no machine address at all the download is refused, with the reason.
func TestTalosEndpointsNeverFallBackToTheClusterEndpoint(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: testNS,
		Spec: ichorv1alpha1.TalosClusterSpec{Endpoint: ichorv1alpha1.TalosClusterEndpoint{Type: ichorv1alpha1.EndpointTypeVIP, Address: epVIP}},
	}

	for name, tc := range map[string]struct {
		machines []client.Object
		want     []string
	}{
		"a live control plane is the endpoint": {
			[]client.Object{endpointTestMachine("cp", roleControlPlane, epCP, false), endpointTestMachine("w", roleWorker, epWorker, false)},
			[]string{epCP},
		},
		"a control plane being deleted still has an address": {
			[]client.Object{endpointTestMachine("cp", roleControlPlane, epCP, true), endpointTestMachine("w", roleWorker, epWorker, false)},
			[]string{epCP, epWorker},
		},
		"only a worker has an address": {
			[]client.Object{endpointTestMachine("w", roleWorker, epWorker, false)},
			[]string{epWorker},
		},
		"no machine at all is refused": {nil, nil},
	} {
		for _, typ := range []string{ichorv1alpha1.EndpointTypeVIP, ichorv1alpha1.EndpointTypeLoadBalancer, ichorv1alpha1.EndpointTypeNode} {
			t.Run(name+"/"+typ, func(t *testing.T) {
				c := cluster.DeepCopy()
				c.Spec.Endpoint.Type = typ

				s := &Server{Client: fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tc.machines...).Build()}

				got, err := s.talosEndpoints(context.Background(), c)

				if tc.want == nil {
					if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "nothing to point a talosconfig at") {
						t.Fatalf("got %v, %v, want a FailedPrecondition that says there is nothing to point it at", got, err)
					}

					return
				}

				if err != nil || strings.Join(got, ",") != strings.Join(tc.want, ",") {
					t.Fatalf("got %v, %v, want %v", got, err, tc.want)
				}
			})
		}
	}
}

// The CA-rotation download builds its talosconfig through buildCredentials, which refuses before it makes any key
// when there is nothing to point the talosconfig at.
func TestBuildCredentialsRefusesWithNoTalosEndpoint(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: testNS,
		Spec: ichorv1alpha1.TalosClusterSpec{Endpoint: ichorv1alpha1.TalosClusterEndpoint{Type: ichorv1alpha1.EndpointTypeVIP, Address: epVIP}},
	}
	s := &Server{Client: fake.NewClientBuilder().WithScheme(testScheme(t)).Build()}

	_, talos, err := s.buildCredentials(context.Background(), cluster, time.Hour, false)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || len(talos) != 0 || strings.Contains(string(talos), epVIP) {
		t.Fatalf("got %v, %q, want a FailedPrecondition and no talosconfig", err, talos)
	}
}
