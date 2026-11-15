package render

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// A setting of the API server written in spec.config reaches the KubeAPIServerConfig document the base
// configuration already carries: the v1alpha1 spelling, which cannot be merged under a multi-document
// version contract, is never written.
func TestAPIServerSettingMergesIntoTheKubeAPIServerDocument(t *testing.T) {
	cluster := testCluster("false")
	m := testMachine("cp1", "controlplane", nil)
	apiServer := &ichorconfig.ClusterApiServer{ExtraArgs: map[string][]string{"audit-log-maxage": {"3"}}}
	patch := configPatch("apiserver", &ichorconfig.Config{Cluster: &ichorconfig.Cluster{ApiServer: apiServer}})

	out := mustRender(t, cluster, m, testBundle(t), []ichorv1alpha1.TalosConfigPatch{patch})

	requireAll(t, out, "kind: KubeAPIServerConfig", "audit-log-maxage")
}
