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

package main

import (
	"context"
	"fmt"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installer"
)

// migratePatchesArg is the argument of the chart's post-upgrade hook Job (dist/chart/templates/hooks). The Job runs this
// image under its own ServiceAccount; the controller process never writes a spec.
const migratePatchesArg = "migrate-patches"

// runMigratePatches moves every TalosConfigPatch from spec.documents to spec.config and returns the exit code.
func runMigratePatches() int {
	cfg, err := ctrl.GetConfig()
	if err == nil {
		var c client.Client
		if c, err = client.New(cfg, client.Options{}); err == nil {
			err = installer.MigrateAllPatches(context.Background(), c, os.Stderr)
		}
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate-patches:", err)

		return 1
	}

	return 0
}
