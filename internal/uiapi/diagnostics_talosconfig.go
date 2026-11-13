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
	"fmt"
	"time"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

const (
	// TalosconfigReaderScope is the subresource of talosclusters the download verb is checked on for the
	// os:reader talosconfig.
	TalosconfigReaderScope = "talosconfig-reader"

	talosconfigMaxBytes    = 64 << 10
	talosconfigContentType = "application/yaml"
)

// DownloadReaderTalosconfig streams a talosconfig whose client certificate is Talos os:reader. The
// key is made here and the controller signs its public half as the signed-in user: the admission webhook
// checks that user's `download` right on talosclusters/talosconfig-reader and records the Event that names
// them. The right is asked here first only so a refusal is clear and nothing is requested for it.
func (s *Server) DownloadReaderTalosconfig(
	ctx context.Context, req *connect.Request[pb.DownloadReaderTalosconfigRequest], stream *connect.ServerStream[pb.DownloadReaderTalosconfigResponse],
) error {
	ref := req.Msg.GetCluster()
	if ref.GetNamespace() == "" || ref.GetName() == "" {
		return invalid("cluster needs a namespace and a name")
	}

	ttl := time.Duration(req.Msg.GetLifetimeSeconds()) * time.Second
	if ttl < 0 || ttl > credentials.MaxTalosconfigLifetime {
		return invalid("the lifetime must be between 0 and %s", credentials.MaxTalosconfigLifetime)
	}

	allowed, err := s.mayDownload(ctx, ref.GetNamespace(), resourceTalosClusters, TalosconfigReaderScope, ref.GetName())
	if err != nil {
		return err
	}

	if !allowed {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not take a talosconfig of cluster %q: Kubernetes RBAC does not grant you the %q verb on talosclusters/%s "+
				"(ichor-operator and ichor-admin hold it)", ref.GetName(), DownloadVerb, TalosconfigReaderScope))
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: ref.GetNamespace(), Name: ref.GetName()}, &cluster); err != nil {
		return mapGetError(fmt.Sprintf("cluster %q", ref.GetName()), err)
	}

	// Before any key is made: with nothing to point the talosconfig at there is nothing to hand out.
	endpoints, err := s.talosEndpoints(ctx, &cluster)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			return err
		}

		return connect.NewError(connect.CodeInternal, err)
	}

	id, err := s.issueAsCaller(ctx, &cluster, ichorv1alpha1.CredentialPurposeTalosconfigReader, "", ttl, "")
	if err != nil {
		if apierrors.IsForbidden(err) {
			return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("the request was refused: %s", truncateText(err.Error(), 300)))
		}

		return connect.NewError(connect.CodeInternal, fmt.Errorf("asking for the talosconfig: %s", truncateText(err.Error(), 300)))
	}

	data, err := credentials.BuildTalosconfig(cluster.Name, endpoints, id.CA, id.Crt, id.Key)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("building the talosconfig: %s", truncateText(err.Error(), 300)))
	}

	w := &chunkWriter{
		send: func(f, c string, d []byte) error {
			return stream.Send(&pb.DownloadReaderTalosconfigResponse{Filename: f, ContentType: c, Data: d})
		},
		max: talosconfigMaxBytes, contentType: talosconfigContentType,
		filename: fmt.Sprintf("talosconfig-reader-%s-%s.yaml", cluster.Name, stamp(s.now())),
	}

	if _, err := w.Write(data); err != nil {
		return downloadCopyError("the talosconfig", ctx, err, talosconfigMaxBytes)
	}

	return w.finish()
}
