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
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

// CA rotation credentials (docs/adr/0093, amending 0091): the operator who works in the UI gets
// the dual-CA kubeconfig and talosconfig from the backend and releases the AwaitingCredentials
// gate. The files are cluster-admin private keys, so the download follows ADR 0084 exactly: a
// Kubernetes RBAC verb checked as the caller, granted to nobody by default, and a Warning Event
// before any byte exists. The acknowledgement only sets the annotation the CLI sets.

const (
	// CredentialsScope is the subresource of talosclusters the download verb is checked on.
	CredentialsScope = "credentials"

	// credentialsReason is the Event reason of a credentials download.
	credentialsReason = "CredentialsDownload"

	credentialsContentType = "application/zip"
	credentialsMaxBytes    = 1 << 20

	credentialsVerifyTimeout = 30 * time.Second
)

// credentialDownloadStages are the stages the credentials may be downloaded in (ADR 0093): from the
// gate until the rotation has replaced the bundle, because an operator who continued without a
// download needs them once their old files stop working at Issuing.
var credentialDownloadStages = []string{
	ichorv1alpha1.CARotationStageAwaitingCredentials, ichorv1alpha1.CARotationStageIssuing,
	ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing,
}

// caRotationOperation reads ref and requires a ca-rotation operation whose stage is one of stages.
func (s *Server) caRotationOperation(ctx context.Context, ref *pb.ObjectRef, stages ...string) (*ichorv1alpha1.TalosOperation, error) {
	op, err := s.getOperation(ctx, ref)
	if err != nil {
		return nil, err
	}

	if op.Spec.Kind != ichorv1alpha1.TalosOperationKindCARotation {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("operation %q is not a ca-rotation", op.Name))
	}

	stage := ""
	if op.Status.CARotation != nil {
		stage = op.Status.CARotation.Stage
	}

	for _, want := range stages {
		if stage == want {
			return op, nil
		}
	}

	return nil, connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("operation %q is at stage %q; this is only possible at %v", op.Name, stage, stages))
}

// mayDownloadCredentials asks Kubernetes whether the caller holds the download verb on
// talosclusters/credentials for the cluster, the same way mayDownload does for machines.
func (s *Server) mayDownloadCredentials(ctx context.Context, op *ichorv1alpha1.TalosOperation) (bool, error) {
	c, err := s.cl(ctx)
	if err != nil {
		return false, connect.NewError(connect.CodeUnavailable, fmt.Errorf("acting as the user: %w", err))
	}

	review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
		Namespace: op.Namespace, Verb: DownloadVerb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: "talosclusters", Subresource: CredentialsScope, Name: op.Spec.ClusterRef.Name,
	}}}

	if err := c.Create(ctx, review); err != nil {
		return false, connect.NewError(connect.CodeUnavailable, fmt.Errorf("asking Kubernetes whether you may: %w", err))
	}

	return review.Status.Allowed, nil
}

// GetCARotationCredentialsPermission reports whether the caller may download the credentials. It only
// decides what the UI shows.
func (s *Server) GetCARotationCredentialsPermission(
	ctx context.Context, req *connect.Request[pb.GetCARotationCredentialsPermissionRequest],
) (*connect.Response[pb.GetCARotationCredentialsPermissionResponse], error) {
	op, err := s.getOperation(ctx, req.Msg.GetOperation())
	if err != nil {
		return nil, err
	}

	allowed, err := s.mayDownloadCredentials(ctx, op)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.GetCARotationCredentialsPermissionResponse{Download: allowed}), nil
}

// emitCredentialsEvent records who started the download. It carries no content, and is created with
// the backend's own client like the diagnostics Event (the users may not create Events).
func (s *Server) emitCredentialsEvent(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, operation string) error {
	now := metav1.NewTime(s.now())
	msg := fmt.Sprintf("CA rotation credentials download started by %s at %s (operation %s)",
		s.actorName(ctx), now.UTC().Format(time.RFC3339), operation)

	ev := &corev1.Event{
		InvolvedObject: corev1.ObjectReference{
			APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: kindTalosCluster,
			Namespace: cluster.Namespace, Name: cluster.Name, UID: cluster.UID,
		},
		Reason: credentialsReason, Message: msg, Type: corev1.EventTypeWarning,
		Source:         corev1.EventSource{Component: "ichor-ui"},
		FirstTimestamp: now, LastTimestamp: now, Count: 1,
	}
	ev.GenerateName, ev.Namespace = cluster.Name+".", cluster.Namespace

	return s.Client.Create(ctx, ev)
}

// buildCredentials makes the kubeconfig and talosconfig of cluster from its secrets bundle and, during
// a rotation, rotation.yaml, with the backend's own client: the keys never pass through the caller's
// identity. The Kubernetes server is the cluster's declared endpoint. Errors carry no key material.
func (s *Server) buildCredentials(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (kubeconfig, talosconfig []byte, err error) {
	if s.ControllerNamespace == "" {
		return nil, nil, errors.New("loading the secrets bundle: the controller namespace is empty")
	}

	src, endpoints, err := credentials.Load(ctx, s.Client, s.ControllerNamespace, cluster)
	if err != nil {
		return nil, nil, err
	}

	if len(endpoints) == 0 {
		endpoints = []string{cluster.Spec.Endpoint.Address}
	}

	server := "https://" + net.JoinHostPort(cluster.Spec.Endpoint.Address, strconv.Itoa(constants.DefaultControlPlanePort))

	kubeconfig, err = src.Kubeconfig(credentials.ServerConfig(cluster.Name, server))
	if err != nil {
		return nil, nil, err
	}

	talosconfig, err = src.Talosconfig(cluster.Name, endpoints)
	if err != nil {
		return nil, nil, err
	}

	return kubeconfig, talosconfig, nil
}

// DownloadCARotationCredentials streams a zip with `kubeconfig` and `talosconfig`.
func (s *Server) DownloadCARotationCredentials(
	ctx context.Context, req *connect.Request[pb.DownloadCARotationCredentialsRequest], stream *connect.ServerStream[pb.DownloadCARotationCredentialsResponse],
) error {
	op, err := s.caRotationOperation(ctx, req.Msg.GetOperation(), credentialDownloadStages...)
	if err != nil {
		return err
	}

	allowed, err := s.mayDownloadCredentials(ctx, op)
	if err != nil {
		return err
	}

	if !allowed {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not download credentials of cluster %q: Kubernetes RBAC does not grant you the %q verb on talosclusters/%s "+
				"(it is not granted to anyone by default; see ichorUI.credentials in the chart)", op.Spec.ClusterRef.Name, DownloadVerb, CredentialsScope))
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &cluster); err != nil {
		return mapGetError(fmt.Sprintf("cluster %q", op.Spec.ClusterRef.Name), err)
	}

	// Audit before any key is read: a download that cannot be recorded does not happen.
	if err := s.emitCredentialsEvent(ctx, &cluster, op.Name); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("the download was refused because its audit Event could not be recorded: %w", err))
	}

	kube, talos, err := s.buildCredentials(ctx, &cluster)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("building the credentials: %s", truncateText(err.Error(), 300)))
	}

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, f := range []struct {
		name string
		data []byte
	}{{"kubeconfig", kube}, {"talosconfig", talos}} {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: s.now()}
		h.SetMode(0o600)

		w, err := zw.CreateHeader(h)
		if err == nil {
			_, err = w.Write(f.data)
		}

		if err != nil {
			return connect.NewError(connect.CodeInternal, errors.New("building the archive failed"))
		}
	}

	if err := zw.Close(); err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("building the archive failed"))
	}

	w := &chunkWriter{
		send: func(f, c string, d []byte) error {
			return stream.Send(&pb.DownloadCARotationCredentialsResponse{Filename: f, ContentType: c, Data: d})
		},
		max: credentialsMaxBytes, contentType: credentialsContentType,
		filename: fmt.Sprintf("credentials-%s-%s.zip", cluster.Name, stamp(s.now())),
	}

	if _, err := w.Write(buf.Bytes()); err != nil {
		return downloadCopyError("credentials", ctx, err, credentialsMaxBytes)
	}

	return w.finish()
}

// AcknowledgeCARotationCredentials verifies that the credentials work, then sets the hand-off
// annotation, as the caller, like SetOperationPaused writes spec.paused.
func (s *Server) AcknowledgeCARotationCredentials(
	ctx context.Context, req *connect.Request[pb.AcknowledgeCARotationCredentialsRequest],
) (*connect.Response[pb.AcknowledgeCARotationCredentialsResponse], error) {
	ref := req.Msg.GetOperation()

	op, err := s.caRotationOperation(ctx, ref, ichorv1alpha1.CARotationStageAwaitingCredentials)
	if err != nil {
		return nil, err
	}

	token := ichorv1alpha1.CARotationHandoffToken(op.Status.CARotation)
	if token == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the new CAs are not known yet"))
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &cluster); err != nil {
		return nil, mapGetError(fmt.Sprintf("cluster %q", op.Spec.ClusterRef.Name), err)
	}

	kube, _, err := s.buildCredentials(ctx, &cluster)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("building the credentials: %s", truncateText(err.Error(), 300)))
	}

	verify := s.VerifyKubeconfig
	if verify == nil {
		verify = credentials.VerifyKubeconfig
	}

	vctx, cancel := context.WithTimeout(ctx, credentialsVerifyTimeout)
	defer cancel()

	if err := verify(vctx, kube); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("the new kubeconfig does not authenticate to the cluster (%s); NOT continuing, so the rotation keeps waiting "+
				"and your current credentials keep working", truncateText(err.Error(), 300)))
	}

	if op.Annotations == nil {
		op.Annotations = map[string]string{}
	}

	op.Annotations[ichorv1alpha1.CredentialsHandoffAnnotation] = token

	if err := s.Update(ctx, op, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("acknowledging the credentials hand-off on operation", namespacedName(ref), err)
	}

	return connect.NewResponse(&pb.AcknowledgeCARotationCredentialsResponse{
		Operation: operationToPB(op), Audit: s.opAudit(ctx, "acknowledge-credentials", ref),
	}), nil
}
