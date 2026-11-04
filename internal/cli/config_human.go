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
	"fmt"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// originText is the layer a value came from and, when a patch set it, that patch: "role/cp-sysctl",
// "system".
func originText(o *pb.ConfigOrigin) string {
	layer := humanEnum(o.GetLayer().String())
	if o.GetSource() == "" || o.GetSource() == layer {
		return layer
	}

	return fmt.Sprintf("%s/%s", layer, o.GetSource())
}

// renderEffectiveConfig prints the rendered configuration as it is, one value per row with the
// layer that set it. Values are the server's JSON, secrets already masked.
func renderEffectiveConfig(h *human, resp *pb.GetEffectiveConfigResponse) error {
	cfg := resp.GetConfig()
	h.Title(nsName(cfg.GetMachine()))

	if len(cfg.GetValues()) == 0 {
		h.Line("no values")

		return h.Err()
	}

	rows := make([][]string, len(cfg.GetValues()))
	for i, v := range cfg.GetValues() {
		rows[i] = []string{v.GetPath(), v.GetValue(), originText(v.GetOrigin())}
	}

	h.Table([]string{"path", "value", "layer"}, rows)

	return h.Err()
}
