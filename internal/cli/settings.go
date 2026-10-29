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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// settingsCmd groups `ichor settings`: the runtime settings in the IchorInstallation, the same
// ones the UI's Settings page edits. Both go through SettingsService, so RBAC, the bounds and the reclaim
// confirmation are judged in one place.
var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show and change Ichor's runtime settings (reclaim, sign-in lifetimes, default drift policy)",
}

var settingsConfirmReclaim bool

var settingsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show each setting, its value in force and where the value comes from (spec or deployment)",
	Long: `Show each setting with the value in force and its source: "spec" when the IchorInstallation
sets it, "deployment" when the flag or chart value applies. The default is a table;
-o yaml or -o json print the full response.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			resp, err := c.Settings.GetSettings(ctx, connect.NewRequest(&pb.GetSettingsRequest{}))
			if err != nil {
				return err
			}

			return writeSettings(cmd, resp.Msg)
		})
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set KEY=VALUE...",
	Short: "Change settings: " + settings.KeyReclaim + ", " + settings.KeyTokenTTL + ", " + settings.KeyRefreshLifetime + ", " + settings.KeyDriftPolicy,
	Long: `Change one or more settings; only the keys you name are written.

  ` + settings.KeyReclaim + `=true|false         break-glass machine reclaim
  ` + settings.KeyTokenTTL + `=15m           token lifetime, 1m to 1h
  ` + settings.KeyRefreshLifetime + `=168h  refresh lifetime, 1h to 720h
  ` + settings.KeyDriftPolicy + `=warnOnly|autoCorrect   default for a new TalosCluster

Enabling reclaim turns on a way to wipe a configured machine; it needs --confirm-reclaim.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := settingsUpdateRequest(args, settingsConfirmReclaim)
		if err != nil {
			return err
		}

		return runAPI(cmd, false, func(ctx context.Context, c *apiClients) error {
			resp, err := c.Settings.UpdateSettings(ctx, connect.NewRequest(req))
			if err != nil {
				return err
			}

			return writeSettings(cmd, resp.Msg.GetSettings())
		})
	},
}

func init() {
	settingsSetCmd.Flags().BoolVar(&settingsConfirmReclaim, "confirm-reclaim", false,
		"confirm that you are enabling break-glass reclaim (required with "+settings.KeyReclaim+"=true)")
	settingsCmd.AddCommand(settingsGetCmd, settingsSetCmd)
	rootCmd.AddCommand(settingsCmd)
}

// settingsUpdateRequest validates KEY=VALUE arguments the way the server will, so a typo fails before any
// connection is made, and refuses to enable reclaim without --confirm-reclaim.
func settingsUpdateRequest(args []string, confirm bool) (*pb.UpdateSettingsRequest, error) {
	c, err := settings.ParseAssignments(args)
	if err != nil {
		return nil, err
	}

	if c.NeedsConfirmation() && !confirm {
		return nil, errors.New("enabling break-glass reclaim needs --confirm-reclaim")
	}

	f := &pb.SettingsFields{ReclaimEnabled: c.Reclaim, DriftPolicy: c.DriftPolicy}
	if c.TokenTTL != nil {
		v := c.TokenTTL.String()
		f.TokenTtl = &v
	}

	if c.RefreshLifetime != nil {
		v := c.RefreshLifetime.String()
		f.RefreshLifetime = &v
	}

	return &pb.UpdateSettingsRequest{Changes: f, ConfirmReclaim: confirm}, nil
}

func writeSettings(cmd *cobra.Command, r *pb.GetSettingsResponse) error {
	if cmd.Flags().Changed("output") {
		return writeMessage(cmd.OutOrStdout(), outputFormat, r)
	}

	return writeSettingsTable(cmd.OutOrStdout(), r)
}

// settingRow is one line of the table.
type settingRow struct{ key, value, source string }

// settingRows lists the facts and the four settings. A value comes from status.effective when the controller has
// written it, else from what spec sets (source spec) or "-" when nothing says.
func settingRows(r *pb.GetSettingsResponse) []settingRow {
	spec, eff := r.GetSpec(), r.GetEffective()
	pick := func(e *pb.SettingValue, fromSpec string) settingRow {
		switch {
		case e != nil:
			return settingRow{value: e.GetValue(), source: e.GetSource()}
		case fromSpec != "":
			return settingRow{value: fromSpec, source: "spec"}
		default:
			return settingRow{value: "-", source: "-"}
		}
	}

	str := func(p *string) string {
		if p == nil {
			return ""
		}

		return *p
	}

	reclaim := ""
	if spec.ReclaimEnabled != nil {
		reclaim = fmt.Sprint(*spec.ReclaimEnabled)
	}

	rows := []settingRow{
		{"version", r.GetVersion(), "spec"},
		{"installedVersion", r.GetInstalledVersion(), "status"},
		{"deployment.manager", r.GetManager(), "spec"},
		pick(eff.GetReclaimEnabled(), reclaim),
		pick(eff.GetTokenTtl(), str(spec.TokenTtl)),
		pick(eff.GetRefreshLifetime(), str(spec.RefreshLifetime)),
		pick(eff.GetDriftPolicy(), str(spec.DriftPolicy)),
	}

	for i, k := range []string{settings.KeyReclaim, settings.KeyTokenTTL, settings.KeyRefreshLifetime, settings.KeyDriftPolicy} {
		rows[i+3].key = k
	}

	return rows
}

func writeSettingsTable(w io.Writer, r *pb.GetSettingsResponse) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "SETTING\tVALUE\tSOURCE")

	for _, row := range settingRows(r) {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", row.key, row.value, row.source)
	}

	if err := tw.Flush(); err != nil {
		return err
	}

	if r.GetReadOnly() {
		_, err := fmt.Fprintln(w, "\nThe settings are managed by the deployment (--settings-read-only); change them there.")

		return err
	}

	return nil
}
