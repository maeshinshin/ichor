package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
)

// The update notice (docs/adr/0114, amendment): after a command that reached a cluster, one line on
// stderr when the cluster's controller found a newer Ichor release, or when this ichor binary is older
// than that release. The CLI makes no request of its own; it reads the IchorInstallation the command's
// client can already read.
const (
	// updateNoticeEnv switches the notice off when it is set to anything non-empty.
	updateNoticeEnv = "ICHOR_NO_UPDATE_NOTICE"
	// updateNoticeInterval is the least time between two notices for one cluster.
	updateNoticeInterval = 24 * time.Hour
	// updateNoticeTimeout bounds the one read that runs after a command.
	updateNoticeTimeout = 3 * time.Second
)

// noticeTarget is the cluster the command last built a client for: the API server host, which names
// the cluster for the throttle, and the client that reads its installation.
type noticeTarget struct {
	Host   string
	Client client.Reader
}

// lastCluster is set by newKubeClientForConfig, the one place every CLI command's client comes from.
var lastCluster noticeTarget

// updateNoticeLines is what to say for an installation running installed whose controller found rel,
// seen by a CLI at cliVersion. Nothing is said while the last check failed or when nothing is newer.
func updateNoticeLines(installed string, rel *ichorv1alpha1.IchorLatestRelease, cliVersion string) []string {
	if rel == nil || rel.Version == "" || rel.Error != "" {
		return nil
	}

	var lines []string

	if releasecheck.Newer(installed, rel.Version) {
		lines = append(lines, fmt.Sprintf(
			"A newer Ichor release is available: %s (installed %s). Upgrade with: ichor upgrade ichor %s",
			rel.Version, installed, rel.Version))
	}

	if releasecheck.Newer(cliVersion, rel.Version) {
		lines = append(lines, fmt.Sprintf(
			"This ichor (%s) is older than the newest Ichor release, %s. Update the ichor binary to %s.",
			cliVersion, rel.Version, rel.Version))
	}

	return lines
}

// updateNoticeFor returns the lines to print after cmd, or nil. It reads the installation only when the
// output could be shown, and it records the cluster in the throttle file only when it is about to show it.
func updateNoticeFor(cmd *cobra.Command, target noticeTarget, cliVersion string, tty bool, now time.Time) []string {
	if !tty || os.Getenv(updateNoticeEnv) != "" || cmd == nil || target.Client == nil || target.Host == "" ||
		outputFlagGiven() || isHelpOrCompletion(cmd) {
		return nil
	}

	path, err := updateNoticePath()
	if err != nil {
		return nil
	}

	shown := loadNoticeTimes(path)
	if last, ok := shown[target.Host]; ok && now.Before(last.Add(updateNoticeInterval)) {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateNoticeTimeout)
	defer cancel()

	var inst ichorv1alpha1.IchorInstallation
	if err := target.Client.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		return nil
	}

	lines := updateNoticeLines(inst.Status.InstalledVersion, inst.Status.LatestRelease, cliVersion)
	if len(lines) == 0 {
		return nil
	}

	shown[target.Host] = now

	// A failed write only means the notice can come back on the next command.
	_ = saveNoticeTimes(path, shown)

	return lines
}

// printUpdateNotice is Execute's hook: it runs after every command, whether the command succeeded or not.
func printUpdateNotice(cmd *cobra.Command) {
	for _, line := range updateNoticeFor(cmd, lastCluster, clientVersion(), stderrIsTerminal(), time.Now()) {
		_, _ = fmt.Fprintln(os.Stderr, line)
	}
}

// outputFlagGiven reports whether -o was typed: then the output is for a script, not for a person.
func outputFlagGiven() bool {
	f := rootCmd.PersistentFlags().Lookup("output")

	return f != nil && f.Changed
}

// isHelpOrCompletion reports whether cmd is a help request or a shell-completion command.
func isHelpOrCompletion(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup("help"); f != nil && f.Changed {
		return true
	}

	for c := cmd; c != nil; c = c.Parent() {
		if n := c.Name(); n == "help" || n == "completion" || strings.HasPrefix(n, "__complete") {
			return true
		}
	}

	return false
}

// stderrIsTerminal reports whether stderr is a character device, which is what a terminal is.
func stderrIsTerminal() bool {
	fi, err := os.Stderr.Stat()

	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// updateNoticePath is ~/.ichor/update-notice.json: when each cluster was last told.
func updateNoticePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory for the update notice: %w", err)
	}

	return filepath.Join(home, ".ichor", "update-notice.json"), nil
}

// loadNoticeTimes reads the throttle file; a missing or unreadable one is empty.
func loadNoticeTimes(path string) map[string]time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]time.Time{}
	}

	var shown map[string]time.Time
	if err := json.Unmarshal(data, &shown); err != nil || shown == nil {
		return map[string]time.Time{}
	}

	return shown
}

// saveNoticeTimes writes the throttle file (writeFileAtomic makes it 0600) in ~/.ichor.
func saveNoticeTimes(path string, shown map[string]time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(shown)
	if err != nil {
		return err
	}

	return writeFileAtomic(path, data)
}
