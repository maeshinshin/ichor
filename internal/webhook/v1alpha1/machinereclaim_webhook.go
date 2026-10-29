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

package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const machineReclaimWebhookPath = "/validate-ichor-maesh-dev-v1alpha1-machinereclaim"

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-machinereclaim,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=machinereclaims,verbs=create;update,versions=v1alpha1,name=vmachinereclaim-v1alpha1.kb.io,admissionReviewVersions=v1

// MachineReclaimHandler guards "Reset and add" (MachineReclaim). Every create and
// every write of spec.confirmUUID needs the custom verb `reclaim` on machinereclaims, asked as the
// requesting user. A confirmUUID must equal status.inspection.uuid. The identity that did each
// is an annotation the writer sets and this webhook insists is the authenticated user, so
// status.audit cannot be forged. There is no installation switch: RBAC alone decides.
type MachineReclaimHandler struct {
	// Client is uncached: it creates SubjectAccessReviews.
	Client client.Client
}

// SetupMachineReclaimWebhookWithManager registers the check on the manager's webhook server.
func SetupMachineReclaimWebhookWithManager(mgr ctrl.Manager) error {
	direct, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return fmt.Errorf("creating the uncached client: %w", err)
	}

	mgr.GetWebhookServer().Register(machineReclaimWebhookPath,
		&admission.Webhook{Handler: &MachineReclaimHandler{Client: direct}})

	return nil
}

// Handle implements admission.Handler.
func (h *MachineReclaimHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	var mr ichorv1alpha1.MachineReclaim
	if err := json.Unmarshal(req.Object.Raw, &mr); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the request: %w", err))
	}

	user := req.UserInfo.Username

	switch req.Operation {
	case opCreate:
		if mr.Spec.ConfirmUUID != "" {
			return admission.Denied("confirmUUID cannot be set on creation: the node has not been inspected yet")
		}

		if got := mr.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation]; got != user {
			return admission.Denied(fmt.Sprintf("annotation %s must be the requesting user %q", ichorv1alpha1.ReclaimRequestedByAnnotation, user))
		}

		return h.needVerb(ctx, req)
	case opUpdate:
		var old ichorv1alpha1.MachineReclaim
		if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the stored request: %w", err))
		}

		if old.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation] != mr.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation] {
			return admission.Denied("the requesting user cannot be changed")
		}

		if mr.Spec.ConfirmUUID == old.Spec.ConfirmUUID || mr.Spec.ConfirmUUID == "" {
			return admission.Allowed("")
		}

		return h.confirm(ctx, req, &old, &mr)
	}

	return admission.Allowed("")
}

func (h *MachineReclaimHandler) confirm(ctx context.Context, req admission.Request, old, mr *ichorv1alpha1.MachineReclaim) admission.Response {
	if old.Status.Phase != ichorv1alpha1.MachineReclaimAwaitingConfirmation || old.Status.Inspection == nil {
		return admission.Denied("confirmUUID can only be written while the request is AwaitingConfirmation")
	}

	if mr.Spec.ConfirmUUID != old.Status.Inspection.UUID {
		return admission.Denied("confirmUUID must equal the node's UUID exactly as inspected (status.inspection.uuid)")
	}

	if got := mr.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation]; got != req.UserInfo.Username {
		return admission.Denied(fmt.Sprintf("annotation %s must be the confirming user %q", ichorv1alpha1.ReclaimConfirmedByAnnotation, req.UserInfo.Username))
	}

	return h.needVerb(ctx, req)
}

func (h *MachineReclaimHandler) needVerb(ctx context.Context, req admission.Request) admission.Response {
	if r := (&BreakGlassHandler{Client: h.Client}).require(ctx, req, ichorv1alpha1.ReclaimVerb, "machinereclaims", "", req.Name,
		fmt.Sprintf("reclaiming a machine needs the %q verb on machinereclaims "+
			"(bind the ClusterRole ichor-machine-reclaimer with a RoleBinding)", ichorv1alpha1.ReclaimVerb)); r != nil {
		return *r
	}

	return admission.Allowed("")
}
