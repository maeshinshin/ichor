package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var noticeNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// noticeHome gives the test its own home directory, so the throttle file is the test's own.
func noticeHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func latestRelease(version string) *ichorv1alpha1.IchorLatestRelease {
	return &ichorv1alpha1.IchorLatestRelease{Version: version}
}

// noticeTargetWith is a cluster at host whose IchorInstallation runs installed and whose controller found rel.
func noticeTargetWith(t *testing.T, host, installed string, rel *ichorv1alpha1.IchorLatestRelease) noticeTarget {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&ichorv1alpha1.IchorInstallation{
		ObjectMeta: metav1.ObjectMeta{Name: ichorv1alpha1.IchorInstallationName},
		Status:     ichorv1alpha1.IchorInstallationStatus{InstalledVersion: installed, LatestRelease: rel},
	}).Build()

	return noticeTarget{Host: host, Client: c}
}

// unreadableTarget is a cluster whose client cannot read IchorInstallation (its scheme lacks the type).
func unreadableTarget(t *testing.T) noticeTarget {
	t.Helper()

	return noticeTarget{Host: "https://broken.example:6443", Client: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()}
}

func plainCmd() *cobra.Command {
	return &cobra.Command{Use: "list"}
}

// markOutputFlag marks -o as typed on the command line, the way the flag parser does.
func markOutputFlag(t *testing.T) {
	t.Helper()

	f := rootCmd.PersistentFlags().Lookup("output")
	f.Changed = true
	t.Cleanup(func() { f.Changed = false })
}

func TestUpdateNoticeLines(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		rel       *ichorv1alpha1.IchorLatestRelease
		cli       string
		want      int
	}{
		{"newer release on the cluster", "v0.1.0", latestRelease("v0.2.0"), "v0.2.0", 1},
		{"installed equals the release", "v0.2.0", latestRelease("v0.2.0"), "v0.2.0", 0},
		{"release older than installed", "v0.2.0", latestRelease("v0.1.0"), "v0.2.0", 0},
		{"no release recorded yet", "v0.1.0", nil, "v0.2.0", 0},
		{"empty release version", "v0.1.0", latestRelease(""), "v0.2.0", 0},
		{"last check failed", "v0.1.0", &ichorv1alpha1.IchorLatestRelease{Version: "v0.2.0", Error: "registry unreachable"}, "v0.2.0", 0},
		{"CLI older than the release", "v0.2.0", latestRelease("v0.2.0"), "v0.1.0", 1},
		{"cluster and CLI both behind", "v0.1.0", latestRelease("v0.2.0"), "v0.1.0", 2},
		{"dev CLI is never prompted", "v0.1.0", latestRelease("v0.2.0"), "dev", 1},
		{"dev cluster is never prompted", "dev", latestRelease("v0.2.0"), "v0.2.0", 0},
		{"demo CLI is behind a final release", "v0.2.0", latestRelease("v0.2.0"), "v0.0.5-gabc", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := updateNoticeLines(tt.installed, tt.rel, tt.cli); len(got) != tt.want {
				t.Fatalf("lines = %q, want %d", got, tt.want)
			}
		})
	}
}

func TestUpdateNoticeTextNamesTheUpgrade(t *testing.T) {
	lines := updateNoticeLines("v0.1.0", latestRelease("v0.2.0"), "v0.2.0")
	want := "A newer Ichor release is available: v0.2.0 (installed v0.1.0). Upgrade with: ichor upgrade ichor v0.2.0"

	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

func TestUpdateNoticeShownOnATerminal(t *testing.T) {
	noticeHome(t)

	target := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))
	if got := updateNoticeFor(plainCmd(), target, "v0.2.0", true, noticeNow); len(got) != 1 {
		t.Fatalf("lines = %q, want the cluster notice", got)
	}
}

func TestUpdateNoticeNotShownWithoutATerminal(t *testing.T) {
	noticeHome(t)

	target := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))
	if got := updateNoticeFor(plainCmd(), target, "v0.2.0", false, noticeNow); got != nil {
		t.Fatalf("lines = %q, want none without a terminal", got)
	}
}

func TestUpdateNoticeSwitchedOffByEnvironment(t *testing.T) {
	noticeHome(t)
	t.Setenv(updateNoticeEnv, "1")

	target := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))
	if got := updateNoticeFor(plainCmd(), target, "v0.2.0", true, noticeNow); got != nil {
		t.Fatalf("lines = %q, want none with %s set", got, updateNoticeEnv)
	}
}

func TestUpdateNoticeNotShownForMachineOutput(t *testing.T) {
	noticeHome(t)
	markOutputFlag(t)

	target := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))
	if got := updateNoticeFor(plainCmd(), target, "v0.2.0", true, noticeNow); got != nil {
		t.Fatalf("lines = %q, want none when -o is given", got)
	}
}

func TestUpdateNoticeNotShownForHelpOrCompletion(t *testing.T) {
	noticeHome(t)

	target := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))

	helpCmd := plainCmd()
	helpCmd.Flags().Bool("help", false, "")
	if err := helpCmd.Flags().Set("help", "true"); err != nil {
		t.Fatal(err)
	}

	completion := &cobra.Command{Use: "completion"}
	bash := &cobra.Command{Use: "bash"}
	completion.AddCommand(bash)

	for name, cmd := range map[string]*cobra.Command{"--help": helpCmd, "help command": {Use: "help"}, "completion": bash} {
		if got := updateNoticeFor(cmd, target, "v0.2.0", true, noticeNow); got != nil {
			t.Fatalf("%s: lines = %q, want none", name, got)
		}
	}
}

func TestUpdateNoticeNotShownWithoutACluster(t *testing.T) {
	noticeHome(t)

	if got := updateNoticeFor(plainCmd(), noticeTarget{}, "v0.2.0", true, noticeNow); got != nil {
		t.Fatalf("lines = %q, want none when the command never reached a cluster", got)
	}
}

func TestUpdateNoticeNotShownWhenTheGetFails(t *testing.T) {
	noticeHome(t)

	if got := updateNoticeFor(plainCmd(), unreadableTarget(t), "v0.2.0", true, noticeNow); got != nil {
		t.Fatalf("lines = %q, want none when the IchorInstallation cannot be read", got)
	}
}

func TestUpdateNoticeThrottledPerClusterForADay(t *testing.T) {
	noticeHome(t)

	a := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))
	b := noticeTargetWith(t, "https://b.example:6443", "v0.1.0", latestRelease("v0.2.0"))

	if got := updateNoticeFor(plainCmd(), a, "v0.2.0", true, noticeNow); len(got) != 1 {
		t.Fatalf("first show of a: lines = %q", got)
	}

	if got := updateNoticeFor(plainCmd(), a, "v0.2.0", true, noticeNow.Add(23*time.Hour)); got != nil {
		t.Fatalf("a again within 24h: lines = %q, want none", got)
	}

	if got := updateNoticeFor(plainCmd(), b, "v0.2.0", true, noticeNow.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("b within 24h of a: lines = %q, want its own notice", got)
	}

	if got := updateNoticeFor(plainCmd(), a, "v0.2.0", true, noticeNow.Add(25*time.Hour)); len(got) != 1 {
		t.Fatalf("a after 24h: lines = %q, want the notice again", got)
	}
}

func TestKubeClientNamesTheClusterForTheNotice(t *testing.T) {
	cfg := &rest.Config{Host: "https://cluster.example:6443"}

	c, err := newKubeClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if lastCluster.Host != cfg.Host || lastCluster.Client != c {
		t.Fatalf("last cluster = %+v, want the client built for %s", lastCluster, cfg.Host)
	}
}

func TestUpdateNoticeRecordsNothingWhenNothingIsShown(t *testing.T) {
	noticeHome(t)

	target := noticeTargetWith(t, "https://a.example:6443", "v0.2.0", latestRelease("v0.2.0"))
	if got := updateNoticeFor(plainCmd(), target, "v0.2.0", true, noticeNow); got != nil {
		t.Fatalf("lines = %q, want none", got)
	}

	path, err := updateNoticePath()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("throttle file exists after a silent run: %v", err)
	}
}

func TestUpdateNoticeThrottleFileIsPrivate(t *testing.T) {
	noticeHome(t)

	target := noticeTargetWith(t, "https://a.example:6443", "v0.1.0", latestRelease("v0.2.0"))
	updateNoticeFor(plainCmd(), target, "v0.2.0", true, noticeNow)

	path, err := updateNoticePath()
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("throttle file mode = %o, want 0600", perm)
	}

	if !strings.HasSuffix(path, "/.ichor/update-notice.json") {
		t.Fatalf("throttle file = %s, want it under ~/.ichor", path)
	}
}
