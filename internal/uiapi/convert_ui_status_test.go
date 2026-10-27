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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The cluster page's Operations section shows the kube-proxy mode, the CA
// expiry dates and the nodes that wait for a reboot, so the wire messages carry them.
func TestClusterToPBCarriesKubeProxyModeAndCertificateExpiry(t *testing.T) {
	notAfter := time.Date(2036, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &ichorv1alpha1.TalosCluster{}
	c.Name, c.Namespace = "c", "ns"
	c.Spec.CNI.KubeProxy = kubeProxyReplaced
	c.Status.Certificates = []ichorv1alpha1.CertificateExpiry{{Name: "kubernetes-aggregator", NotAfter: metav1.NewTime(notAfter)}}

	got := clusterToPB(c)
	if got.GetKubeProxyMode() != kubeProxyReplaced {
		t.Fatalf("kube-proxy mode = %q", got.GetKubeProxyMode())
	}

	certs := got.GetCertificates()
	if len(certs) != 1 || certs[0].GetName() != "kubernetes-aggregator" || !certs[0].GetNotAfter().AsTime().Equal(notAfter) {
		t.Fatalf("certificates = %v", certs)
	}
}

func TestMachineToPBCarriesNodeName(t *testing.T) {
	m := &ichorv1alpha1.TalosMachine{}
	m.Status.NodeName = "ichor-cp-1"

	if got := machineToPB(m).GetNodeName(); got != "ichor-cp-1" {
		t.Fatalf("node name = %q", got)
	}
}

func TestMachineToPBCarriesRebootPaths(t *testing.T) {
	m := &ichorv1alpha1.TalosMachine{}
	m.Status.RebootPaths = []string{"machine.install"}

	if got := machineToPB(m).GetRebootPaths(); len(got) != 1 || got[0] != "machine.install" {
		t.Fatalf("reboot paths = %v", got)
	}
}
