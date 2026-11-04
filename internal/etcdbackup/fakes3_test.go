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

package etcdbackup

import (
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is the smallest S3 that minio-go's PutObject, GetObject (with
// Stat), RemoveObject and ListObjectsV2 talk to, behind a TLS httptest
// server. It proves the real minio client works against the protocol shape;
// internal/controller's tests use an in-memory Store instead.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte // "bucket/key" -> body
	srv     *httptest.Server
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()

	f := &fakeS3{objects: map[string][]byte{}}
	f.srv = httptest.NewTLSServer(f)
	t.Cleanup(f.srv.Close)

	return f
}

// store is a Store on the minio client, pointed at f.
func (f *fakeS3) store(t *testing.T) Store {
	t.Helper()

	st, err := NewS3Store(S3Config{
		Endpoint: f.srv.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "AK", SecretAccessKey: "SK", CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw}),
	})
	if err != nil {
		t.Fatal(err)
	}

	return st
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/")

	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		bucket := strings.TrimSuffix(path, "/")
		prefix := r.URL.Query().Get("prefix")

		var keys []string

		for k := range f.objects {
			if rest, ok := strings.CutPrefix(k, bucket+"/"); ok && strings.HasPrefix(rest, prefix) {
				keys = append(keys, rest)
			}
		}

		slices.Sort(keys)

		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
		fmt.Fprintf(&b, "<Name>%s</Name><Prefix>%s</Prefix><KeyCount>%d</KeyCount><IsTruncated>false</IsTruncated>", bucket, prefix, len(keys))

		for _, k := range keys {
			fmt.Fprintf(&b, "<Contents><Key>%s</Key><Size>%d</Size><LastModified>2026-01-01T00:00:00.000Z</LastModified></Contents>",
				k, len(f.objects[bucket+"/"+k]))
		}

		b.WriteString("</ListBucketResult>")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, b.String())
	case r.Method == http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objects[path] = body
		w.Header().Set("ETag", `"fake"`)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		body, ok := f.objects[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`)

			return
		}

		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2026 00:00:00 GMT")
		w.Header().Set("ETag", `"fake"`)

		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	case r.Method == http.MethodDelete:
		delete(f.objects, path)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}
