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

package uiapi

import (
	"context"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// DismissCARotationAdvice sets the dismissal annotation on the TalosCluster and writes nothing
// else (ADR 0103, section 1.4). The controller clears the CARotationAdvised condition.
func (s *Server) DismissCARotationAdvice(
	ctx context.Context, req *connect.Request[pb.DismissCARotationAdviceRequest],
) (*connect.Response[pb.DismissCARotationAdviceResponse], error) {
	key := namespacedName(req.Msg.GetCluster())

	c, err := s.loadCluster(ctx, key)
	if err != nil {
		return nil, err
	}

	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}

	c.Annotations[ichorv1alpha1.TalosClusterCARotationAdviceDismissedAnnotation] = "true"

	if err := s.Update(ctx, c, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("dismissing the CA rotation advice of", key, err)
	}

	return connect.NewResponse(&pb.DismissCARotationAdviceResponse{
		Audit: s.opAudit(ctx, "dismiss-ca-rotation-advice", objectRef(c.Namespace, c.Name)),
	}), nil
}
