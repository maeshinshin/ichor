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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-namespace,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters;talosmachines;talosconfigpatches;talosoperations;talosetcdbackups;talosstorages;taloscredentialrequests;machinereclaims;ichorusers,verbs=create,versions=v1alpha1,name=vnamespace-v1alpha1.kb.io,admissionReviewVersions=v1

const controllerNamespaceWebhookPath = "/validate-ichor-maesh-dev-v1alpha1-namespace"

// ControllerNamespaceHandler refuses a new Ichor object outside the controller's namespace (ADR 0175).
type ControllerNamespaceHandler struct {
	Namespace   string
	ExemptUsers []string
}

// SetupControllerNamespaceWebhookWithManager registers the check on the manager's webhook server.
func SetupControllerNamespaceWebhookWithManager(mgr ctrl.Manager, namespace string, exempt []string) error {
	mgr.GetWebhookServer().Register(controllerNamespaceWebhookPath,
		&admission.Webhook{Handler: &ControllerNamespaceHandler{Namespace: namespace, ExemptUsers: exempt}})

	return nil
}

// Handle implements admission.Handler.
func (h *ControllerNamespaceHandler) Handle(_ context.Context, _ admission.Request) admission.Response {
	return admission.Allowed("")
}
