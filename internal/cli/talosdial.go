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
	"fmt"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/client"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
)

// talosConfigReader is the one Talos API call adopt needs: a node's current
// configuration, read-only. adopt never mutates a Talos node -- it only
// ever reads from one to compute a CR (docs/design/architecture.md, "CLI
// and UI": the CLI changes the world by writing CRs) -- so this interface
// has no apply/dry-run method to tempt it into doing otherwise.
type talosConfigReader interface {
	CurrentConfig(ctx context.Context) ([]byte, error)
}

// dialTalos connects to one node's Talos API the way talosctl itself does:
// through the admin identity already recorded in the talosconfig at path
// ("" lets machinery resolve $TALOSCONFIG and ~/.talos/config itself),
// rather than re-deriving a client certificate from a secrets bundle the
// way the controller's NewMachineryTalosClient does for a cluster Ichor
// already manages. adopt predates that management, so the
// talosconfig the operator already uses for this cluster is what it reads.
func dialTalos(ctx context.Context, talosconfigPath, endpoint string) (talosConfigReader, error) {
	c, err := client.New(ctx, client.WithConfigFromFile(talosconfigPath), client.WithEndpoints(endpoint))
	if err != nil {
		return nil, fmt.Errorf("dialing Talos API at %q: %w", endpoint, err)
	}

	return &talosClient{c: c}, nil
}

// talosClient adapts a real *client.Client to talosConfigReader.
type talosClient struct {
	c *client.Client
}

// Close releases the gRPC connection.
func (t *talosClient) Close() error {
	return t.c.Close()
}

// CurrentConfig reads the MachineConfig COSI resource's active ID, the same
// way internal/controller's machineryTalosClient does -- its Bytes() method
// returns the node's configuration exactly as last submitted, directly
// comparable to pkg/render's output.
func (t *talosClient) CurrentConfig(ctx context.Context) ([]byte, error) {
	md := resource.NewMetadata(configres.NamespaceName, configres.MachineConfigType, configres.ActiveID, resource.VersionUndefined)

	res, err := safe.StateGet[*configres.MachineConfig](ctx, t.c.COSI, md)
	if err != nil {
		return nil, fmt.Errorf("reading current machine configuration: %w", err)
	}

	return res.Provider().Bytes()
}
