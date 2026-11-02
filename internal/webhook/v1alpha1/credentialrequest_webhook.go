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

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const credentialRequestWebhookPath = "/validate-ichor-maesh-dev-v1alpha1-taloscredentialrequest"

// downloadVerb is the verb ADR 0084 and ADR 0093 gate the downloads with; the credential request reuses it
// so a binding that grants a download also grants the certificate that download needs.
const downloadVerb = "download"

const credentialClusters = "talosclusters"

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-taloscredentialrequest,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=taloscredentialrequests,verbs=create,versions=v1alpha1,name=vtaloscredentialrequest-v1alpha1.kb.io,admissionReviewVersions=v1

// CredentialRequestHandler is the gate in front of the controller's signing key (ADR 0108). A
// TalosCredentialRequest asks the controller, which alone reads the secrets bundle, for a certificate; the
// controller signs whatever it is given, so this webhook decides who may ask for what, with a
// SubjectAccessReview as the requesting user: the download verbs for the three downloads and the operator
// credentials, and the verb `issue` (held by ichor-ui's ServiceAccount only) for the two purposes the
// backend needs for its own reads. A request that fails the check never exists.
type CredentialRequestHandler struct {
	// Client is uncached: it creates SubjectAccessReviews and reads the named machine.
	Client client.Client
}

// SetupCredentialRequestWebhookWithManager registers the check on the manager's webhook server.
func SetupCredentialRequestWebhookWithManager(mgr ctrl.Manager) error {
	direct, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return fmt.Errorf("creating the uncached client: %w", err)
	}

	mgr.GetWebhookServer().Register(credentialRequestWebhookPath,
		&admission.Webhook{Handler: &CredentialRequestHandler{Client: direct}})

	return nil
}

// credentialPermission is the Kubernetes permission a purpose needs.
func credentialPermission(cr *ichorv1alpha1.TalosCredentialRequest) (verb, resource, subresource, name string, ok bool) {
	cluster := cr.Spec.ClusterRef.Name

	switch cr.Spec.Purpose {
	case ichorv1alpha1.CredentialPurposePublic:
		return ichorv1alpha1.CredentialIssueVerb, credentialClusters, "public", cluster, true
	case ichorv1alpha1.CredentialPurposeNodeReader:
		return ichorv1alpha1.CredentialIssueVerb, credentialClusters, "node-reader", cluster, true
	case ichorv1alpha1.CredentialPurposeCredentialsTalos, ichorv1alpha1.CredentialPurposeCredentialsKubernetes:
		return downloadVerb, credentialClusters, "credentials", cluster, true
	case ichorv1alpha1.CredentialPurposeSupportBundle, ichorv1alpha1.CredentialPurposeEtcdSnapshot,
		ichorv1alpha1.CredentialPurposePacketCapture:
		if cr.Spec.MachineRef == nil {
			return "", "", "", "", false
		}

		return downloadVerb, "talosmachines", string(cr.Spec.Purpose), cr.Spec.MachineRef.Name, true
	default:
		return "", "", "", "", false
	}
}

// Handle implements admission.Handler.
func (h *CredentialRequestHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != opCreate {
		return admission.Allowed("")
	}

	var cr ichorv1alpha1.TalosCredentialRequest
	if err := json.Unmarshal(req.Object.Raw, &cr); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the request: %w", err))
	}

	verb, resource, subresource, name, ok := credentialPermission(&cr)
	if !ok {
		return admission.Denied(fmt.Sprintf("purpose %q needs a machineRef or is unknown", cr.Spec.Purpose))
	}

	// A machine's permission must be for a machine of the cluster whose CA signs.
	if cr.Spec.MachineRef != nil {
		var m ichorv1alpha1.TalosMachine

		key := types.NamespacedName{Namespace: req.Namespace, Name: cr.Spec.MachineRef.Name}
		if err := h.Client.Get(ctx, key, &m); err != nil {
			return admission.Denied(fmt.Sprintf("machine %q cannot be read", key.Name))
		}

		if m.Spec.ClusterRef.Name != cr.Spec.ClusterRef.Name {
			return admission.Denied(fmt.Sprintf("machine %q is not a machine of cluster %q", key.Name, cr.Spec.ClusterRef.Name))
		}
	}

	if r := (&BreakGlassHandler{Client: h.Client}).require(ctx, req, verb, resource, subresource, name,
		fmt.Sprintf("a %s certificate needs the %q verb on %s/%s (%s); it is not granted by default",
			cr.Spec.Purpose, verb, resource, subresource, name)); r != nil {
		return *r
	}

	return admission.Allowed("")
}
