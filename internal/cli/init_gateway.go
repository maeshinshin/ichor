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
	"io"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/pkg/cni/gatewayapi"
)

// gatewayAPIFieldOwner is the server-side-apply manager of the Gateway API CRDs. It is not the installer's, so an
// `ichor upgrade` never owns them and never removes what a release drops.
const gatewayAPIFieldOwner = "ichor-init-gateway-api"

// applyGatewayAPICRDs applies the Gateway API CRDs Cilium's gatewayAPI needs, through the API server once it
// answers, as `ichor init --gateway-api` does. They are in no Talos configuration, so no CNI hand-over can drop
// them, and this only applies: it never deletes, and an existing CRD is updated in place.
func applyGatewayAPICRDs(ctx context.Context, k8sClient client.Client, warn io.Writer) error {
	objs, err := installer.Decode(gatewayapi.CRDs())
	if err != nil {
		return fmt.Errorf("init: decoding the Gateway API CRDs: %w", err)
	}

	for _, obj := range objs {
		err := retryInit(ctx, warn, func() error {
			return k8sClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj),
				client.FieldOwner(gatewayAPIFieldOwner), client.ForceOwnership)
		})
		if err != nil {
			return fmt.Errorf("init: applying Gateway API CRD %q: %w", obj.GetName(), err)
		}
	}

	_, _ = fmt.Fprintf(warn, "init: applied the Gateway API CRDs (%d)\n", len(objs))

	return nil
}
