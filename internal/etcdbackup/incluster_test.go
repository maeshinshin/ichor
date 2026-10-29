package etcdbackup

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestInClusterReason(t *testing.T) {
	custom := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		Network: &ichorv1alpha1.TalosClusterNetwork{PodSubnets: []string{"172.20.0.0/16"}, ServiceSubnets: []string{"172.21.0.0/16"}},
	}}
	plain := &ichorv1alpha1.TalosCluster{}

	for _, tc := range []struct {
		name, endpoint string
		cluster        *ichorv1alpha1.TalosCluster
		inCluster      bool
	}{
		{"svc", "http://minio.minio.svc:9000", plain, true},
		{"svc cluster.local", "http://minio.minio.svc.cluster.local:9000", plain, true},
		{"trailing dot and case", "https://MINIO.minio.svc.cluster.local.:9000", plain, true},
		{"default service IP", "http://10.96.12.5:9000", plain, true},
		{"default pod IP", "http://10.244.1.7:9000", plain, true},
		{"custom pod range", "http://172.20.3.4:9000", custom, true},
		{"custom service range", "http://172.21.0.9", custom, true},
		{"default range is replaced by a declared one", "http://10.96.12.5:9000", custom, false},
		{"outside IP", "http://192.168.1.50:9000", plain, false},
		{"public host", "https://s3.eu-west-1.amazonaws.com", plain, false},
		{"a host that merely contains svc", "http://svc.example.com", plain, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := InClusterReason(tc.endpoint, tc.cluster)
			if (got != "") != tc.inCluster {
				t.Fatalf("InClusterReason(%q) = %q, want in cluster %v", tc.endpoint, got, tc.inCluster)
			}
		})
	}

	if m := InClusterMessage("x"); !strings.Contains(m, "must live outside the cluster being restored") {
		t.Fatalf("message = %q", m)
	}
}
