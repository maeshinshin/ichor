package uiapi

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// GetIchorInstallation reports what the controller's release check last found, and whether it is
// newer than the running version.
func TestGetIchorInstallationReportsTheReleaseCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		installed, latest, errText string
		available                  bool
	}{
		"newer release found":         {ichorOld, ichorNew, "", true},
		"running the newest release":  {ichorNew, ichorNew, "", false},
		"failed check keeps the last": {ichorOld, ichorNew, "registry unreachable", true},
	} {
		t.Run(name, func(t *testing.T) {
			inst := ichorInstallation(ichorOld, tc.installed)
			inst.Status.LatestRelease = &ichorv1alpha1.IchorLatestRelease{
				Version: tc.latest, CheckedAt: &metav1.Time{Time: time.Now()}, Error: tc.errText,
			}

			srv, _ := opServer(t, inst)

			res, err := srv.GetIchorInstallation(context.Background(), connect.NewRequest(&pb.GetIchorInstallationRequest{}))
			if err != nil {
				t.Fatal(err)
			}

			m := res.Msg
			if m.GetLatestVersion() != tc.latest || m.GetReleaseAvailable() != tc.available ||
				m.GetReleaseCheckError() != tc.errText || m.GetReleaseCheckedAt() == nil {
				t.Fatalf("got %v", m)
			}
		})
	}
}
