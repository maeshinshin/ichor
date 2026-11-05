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

package controller

import (
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// refusalNote says, when err is an API refusing the credentials it was given, that this is what happened, so a
// rotation stalled on it does not read like one waiting for a kube-apiserver. On hardware the old service-account
// key was dropped under Ichor's own controller; its token was refused (401, "the server has asked for the client to
// provide credentials") and the operation kept showing the message of the last step that had worked. "" for any other error.
func refusalNote(err error) string {
	switch {
	case err == nil:
		return ""
	case status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied:
		return " (the Talos API refused the credentials Ichor presented, so the state of the node could not be read: this is not a " +
			"kube-apiserver that has not restarted)"
	case apierrors.IsUnauthorized(err) || strings.Contains(err.Error(), "provide credentials"):
		return " (the Kubernetes API refused the credentials presented, so its state could not be read: this is not a kube-apiserver " +
			"that has not restarted. If these are Ichor's own ServiceAccount token, it was signed by a key the API server no longer " +
			"accepts: restart the Ichor controller Deployment, or wait up to 48 minutes for the kubelet to refresh the token)"
	}

	return ""
}
