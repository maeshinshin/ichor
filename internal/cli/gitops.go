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

import "github.com/spf13/cobra"

// gitopsCmd groups the GitOps-adjacent subcommands (docs/design/
// architecture.md, "GitOps and Argo CD"): export writes the repository
// skeleton for an existing cluster; handoff triggers a manual CNI
// hand-over. Neither applies anything to a Talos node; both only ever
// read the cluster's CRs or write a YAML file or a CR field, matching
// the CLI/controller split everywhere else in this project.
var gitopsCmd = &cobra.Command{
	Use:   "gitops",
	Short: "Export CRs as a GitOps repository skeleton, or trigger a manual hand-over",
}

func init() {
	rootCmd.AddCommand(gitopsCmd)
}
