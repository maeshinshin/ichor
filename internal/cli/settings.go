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
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// settingsCmd groups `ichor settings`: the runtime settings in the IchorInstallation, the same
// ones the UI's Settings page edits. Both go through SettingsService, so RBAC and the bounds are judged in one place.
var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show and change Ichor's runtime settings (sign-in lifetimes, default drift policy, update check, automatic patching)",
}

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
	Short: "Change settings (see --help for the keys)",
	Long: `Change one or more settings; only the keys you name are written.

  ` + settings.KeyTokenTTL + `=15m           token lifetime, 1m to 1h
  ` + settings.KeyRefreshLifetime + `=168h  refresh lifetime, 1h to 720h
  ` + settings.KeyDriftPolicy + `=warnOnly|autoCorrect   default for a new TalosCluster
  ` + settings.KeyUpdateCheck + `=true|false    look for newer releases
  ` + settings.KeyAutoPatchEnabled + `=true|false    apply patch releases of the running minor by itself
  ` + settings.KeyAutoPatchComponents + `=talos,cilium,kubernetes   which components (empty: all)
  ` + settings.KeyAutoPatchWindow + `=02:00-05:00   daily UTC window an automatic upgrade may start in (empty: any time)
  ` + settings.KeyAutoPatchAckHalt + `=NAMESPACE/NAME   resume after an automatic operation halted auto-patching

reclaim.enabled is removed: who may reset and add a machine is decided by RBAC alone.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := settingsUpdateRequest(args)
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
	settingsCmd.AddCommand(settingsGetCmd, settingsSetCmd)
	rootCmd.AddCommand(settingsCmd)
}

// settingsUpdateRequest validates KEY=VALUE arguments the way the server will, so a typo fails before any
// connection is made.
func settingsUpdateRequest(args []string) (*pb.UpdateSettingsRequest, error) {
	c, err := settings.ParseAssignments(args)
	if err != nil {
		return nil, err
	}

	f := &pb.SettingsFields{
		DriftPolicy: c.DriftPolicy, UpdateCheckEnabled: c.UpdateCheck, AutoPatchEnabled: c.AutoPatchEnabled,
		AutoPatchWindow: c.AutoPatchWindow, AutoPatchAcknowledgedHalt: c.AutoPatchAcknowledge,
	}

	if c.AutoPatchComponents != nil {
		v := strings.Join(c.AutoPatchComponents, ",")
		f.AutoPatchComponents = &v
	}

	if c.TokenTTL != nil {
		v := c.TokenTTL.String()
		f.TokenTtl = &v
	}

	if c.RefreshLifetime != nil {
		v := c.RefreshLifetime.String()
		f.RefreshLifetime = &v
	}

	return &pb.UpdateSettingsRequest{Changes: f}, nil
}

func writeSettings(cmd *cobra.Command, r *pb.GetSettingsResponse) error {
	if outputFormat != outputHuman {
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
			return settingRow{value: fromSpec, source: sourceSpec}
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

	rows := []settingRow{
		{"version", r.GetVersion(), sourceSpec},
		{"installedVersion", r.GetInstalledVersion(), sourceStatus},
		{"deployment.manager", r.GetManager(), sourceSpec},
		pick(eff.GetTokenTtl(), str(spec.TokenTtl)),
		pick(eff.GetRefreshLifetime(), str(spec.RefreshLifetime)),
		pick(eff.GetDriftPolicy(), str(spec.DriftPolicy)),
		pick(eff.GetUpdateCheck(), ""),
		pick(eff.GetAutoPatch(), ""),
		pick(eff.GetAutoPatchComponents(), str(spec.AutoPatchComponents)),
		pick(eff.GetAutoPatchWindow(), str(spec.AutoPatchWindow)),
	}

	keys := []string{
		settings.KeyTokenTTL, settings.KeyRefreshLifetime, settings.KeyDriftPolicy, settings.KeyUpdateCheck,
		settings.KeyAutoPatchEnabled, settings.KeyAutoPatchComponents, settings.KeyAutoPatchWindow,
	}
	for i, k := range keys {
		rows[i+3].key = k
	}

	if h := r.GetAutoPatchHaltedOperation(); h != "" {
		rows = append(rows, settingRow{"autoPatch.halted", h, sourceStatus})
	}

	return rows
}

func writeSettingsTable(w io.Writer, r *pb.GetSettingsResponse) error {
	h := newHuman(w)

	rows := [][]string{}
	for _, row := range settingRows(r) {
		rows = append(rows, []string{row.key, row.value, row.source})
	}

	h.Table([]string{"setting", "value", "source"}, rows)

	if r.GetReadOnly() {
		h.Note("")
		h.Note(h.Mark(markWarning) + " The settings are managed by the deployment (--settings-read-only); change them there.")
	}

	return h.Err()
}

// The source column of the table: where a row's value is read from.
const (
	sourceSpec   = "spec"
	sourceStatus = "status"
)
