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
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// EtcdSnapshotPath is where the UI posts a form to save a snapshot.
const EtcdSnapshotPath = "/download/etcd-snapshot"

const maxSnapshotFormBytes = 64 << 10

// EtcdSnapshotHandler serves a snapshot as a plain HTTP download for the browser, which saves it to
// disk as it arrives: neither ichor-ui nor the page holds more than one copy buffer of it.
// A navigation cannot send a header, so the form carries the ID token in its POST body (never a URL,
// so no log or history holds it); no cookie is involved, so a page on another site cannot start a
// download. The checks are those of DownloadEtcdSnapshot, made as the same signed-in user.
func (s *Server) EtcdSnapshotHandler(authenticate func(ctx context.Context, header string) (Identity, error), l *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The refusal is shown in the hidden frame the form targets, so it may be framed by this origin.
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Cache-Control", "no-store")

		fail := func(err error) {
			err = sanitizer{l: l}.clean(EtcdSnapshotPath, err)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(httpStatus(connect.CodeOf(err)))
			_, _ = io.WriteString(w, err.Error()+"\n")
		}

		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "use POST", http.StatusMethodNotAllowed)

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxSnapshotFormBytes)
		if err := r.ParseForm(); err != nil {
			fail(connect.NewError(connect.CodeInvalidArgument, errors.New("bad form")))
			return
		}

		id, err := authenticate(r.Context(), "Bearer "+r.PostForm.Get("authorization"))
		if err != nil {
			fail(connect.NewError(connect.CodeUnauthenticated, err))
			return
		}

		if id.MustChange {
			fail(connect.NewError(connect.CodePermissionDenied, errors.New("change your password first")))
			return
		}

		// The identity is checked once, here and in openEtcdSnapshot: the certificate is then issued and the
		// download audited, so the transfer is not tied to the token's end (as with the RPC); the idle period bounds it,
		// on the read side (idleReader) and, through deadlineWriter, on a client that stops reading.
		ctx := ContextWithIdentity(r.Context(), id)
		w = deadlineWriter{w}

		ref := &pb.ObjectRef{Namespace: r.PostForm.Get("namespace"), Name: r.PostForm.Get("name")}

		ctx, rc, filename, done, err := s.openEtcdSnapshot(ctx, ref)
		if err != nil {
			fail(err)
			return
		}

		defer done()

		h := w.Header()
		h.Set("Content-Type", etcdSnapshotContentType)
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		h.Set("X-Content-Type-Options", "nosniff")

		// The status goes out now: io.Copy would otherwise buffer the first chunk, as the wrapper hides ReadFrom.
		_ = http.NewResponseController(w).Flush()

		// An error after the first byte cannot change the status: aborting the response makes the browser
		// report a failed download instead of saving a short file as a whole one.
		if _, err := io.Copy(w, rc); err != nil {
			l.Printf("etcd snapshot of %q/%q stopped: %v (context: %v)", ref.GetNamespace(), ref.GetName(), err, ctx.Err())
			panic(http.ErrAbortHandler)
		}
	})
}

// deadlineWriter gives every write and flush its own deadline of etcdSnapshotIdle, so a client that stops
// reading ends the handler (and frees its download slot) instead of blocking it for good. The server sets no
// WriteTimeout, which would cut a long download that is still moving; cancelling the request's context does not
// unblock a write already waiting on the connection.
type deadlineWriter struct{ http.ResponseWriter }

// bound runs one write or flush under the idle deadline and lifts it after, so the wait for the next chunk
// is not charged to a write that has not started.
func (w deadlineWriter) bound(op func(*http.ResponseController) error) error {
	rc := http.NewResponseController(w.ResponseWriter)
	_ = rc.SetWriteDeadline(time.Now().Add(etcdSnapshotIdle)) // unsupported (an HTTP/1 test recorder): the write is then unbounded
	err := op(rc)
	_ = rc.SetWriteDeadline(time.Time{})

	return err
}

func (w deadlineWriter) Write(p []byte) (n int, err error) {
	err = w.bound(func(*http.ResponseController) (err error) { n, err = w.ResponseWriter.Write(p); return })

	return n, err
}

func (w deadlineWriter) FlushError() error {
	return w.bound(func(rc *http.ResponseController) error { return rc.Flush() })
}

func (w deadlineWriter) Flush() { _ = w.FlushError() }

func (w deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// WithWriteDeadline wraps h so each response write has the idle deadline of deadlineWriter: for the streaming RPCs.
func WithWriteDeadline(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(deadlineWriter{w}, r) })
}

func httpStatus(c connect.Code) int {
	switch c { //nolint:exhaustive // the rest is a server fault
	case connect.CodeInvalidArgument:
		return http.StatusBadRequest
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeFailedPrecondition:
		return http.StatusConflict
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	}

	return http.StatusInternalServerError
}
