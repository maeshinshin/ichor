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

// imagesCmd groups the air-gap-adjacent image listing subcommands
// (docs/design/architecture.md, "Monitoring and air gap").
var imagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List the images a given Talos version (and schematic) needs mirrored for an air-gapped install",
}

func init() {
	rootCmd.AddCommand(imagesCmd)
}
