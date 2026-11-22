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
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// keyTimeLayout is the timestamp in an object name: sortable as a string,
// no colons (awkward in some S3 gateways), always UTC.
const keyTimeLayout = "20060102T150405Z"

var keyPattern = regexp.MustCompile(`^etcd-(\d{8}T\d{6}Z)\.snapshot\.age$`)

// ObjectName is the base name of a snapshot taken at t. Restore picks the
// newest snapshot by listing the prefix and sorting these names.
func ObjectName(t time.Time) string {
	return "etcd-" + t.UTC().Format(keyTimeLayout) + ".snapshot.age"
}

// ParseObjectName returns the time encoded in a base name, or false if name
// is not one of ours (anything else under the prefix is never touched).
func ParseObjectName(name string) (time.Time, bool) {
	m := keyPattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, false
	}

	t, err := time.Parse(keyTimeLayout, m[1])

	return t, err == nil
}

// Object is one stored snapshot.
type Object struct {
	Key  string    // full key, prefix included
	Name string    // base name
	Time time.Time // from the name: anyone who can write a key can set it
	Size int64

	// Modified is the store's own last-modified time (S3 LastModified), set by the
	// server when the object is written. Zero when the store gives none.
	Modified time.Time
}

// Store is the object storage a backup goes to. Keys are always
// "<prefix>/<ObjectName>".
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// List returns our snapshots under prefix, newest first. Foreign objects are skipped.
	List(ctx context.Context, prefix string) ([]Object, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// S3Config addresses a bucket. AccessKeyID, SecretAccessKey, SessionToken and
// the Secret behind them never leave this struct except into the S3 client.
type S3Config struct {
	Endpoint string // https://host[:port] or http://host[:port]
	Region   string
	Bucket   string

	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	CAPEM           []byte // optional extra root CAs for Endpoint
}

type s3Store struct {
	c      *minio.Client
	bucket string
}

// NewS3Store builds a Store on minio-go. It makes no request.
func NewS3Store(cfg S3Config) (Store, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("s3 endpoint must be an http:// or https:// URL")
	}

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken),
		Secure: u.Scheme == "https",
		Region: cfg.Region,
	}

	if len(cfg.CAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CAPEM) {
			return nil, errors.New("s3 ca.crt holds no PEM certificate")
		}

		tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // stdlib guarantees *http.Transport
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		opts.Transport = tr
	}

	c, err := minio.New(u.Host, opts)
	if err != nil {
		return nil, fmt.Errorf("creating s3 client: %w", err)
	}

	return &s3Store{c: c, bucket: cfg.Bucket}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: "application/octet-stream"})

	return err
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, s.classify(key, err)
	}

	// GetObject is lazy: surface a missing key now rather than on first Read.
	if _, err := o.Stat(); err != nil {
		_ = o.Close()

		return nil, s.classify(key, err)
	}

	return o, nil
}

// classify wraps the answers of S3 that a retry cannot change: a missing bucket or key in ErrNotFound and a
// refused credential in ErrDenied, each naming the bucket and key. Any other error is returned as it is.
func (s *s3Store) classify(key string, err error) error {
	r := minio.ToErrorResponse(err)

	switch {
	case r.Code == "NoSuchKey" || r.Code == "NoSuchBucket" || r.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s in bucket %s: %w", ErrNotFound, key, s.bucket, err)
	case r.Code == "AccessDenied" || r.Code == "InvalidAccessKeyId" || r.Code == "SignatureDoesNotMatch" ||
		r.StatusCode == http.StatusUnauthorized || r.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s in bucket %s: %w", ErrDenied, key, s.bucket, err)
	}

	return err
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	return s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *s3Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object

	for o := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix + "/"}) {
		if o.Err != nil {
			return nil, o.Err
		}

		name := o.Key[len(prefix)+1:]
		if t, ok := ParseObjectName(name); ok {
			out = append(out, Object{Key: o.Key, Name: name, Time: t, Size: o.Size, Modified: o.LastModified})
		}
	}

	slices.SortFunc(out, func(a, b Object) int { return b.Time.Compare(a.Time) })

	return out, nil
}

// Prune deletes snapshots under prefix until keep remain, counting justWritten
// (the key the caller has just uploaded) as one of them. justWritten is never
// deleted, whatever its name says: a clock that stepped backwards names it
// older than snapshots that were taken before it. The rest of the keep are the
// newest by the store's Modified time; the name's time only breaks ties, since
// a key named for the far future would otherwise outrank every real snapshot.
// A failed delete is returned after trying the rest.
func Prune(ctx context.Context, s Store, prefix, justWritten string, keep int) error {
	objs, err := s.List(ctx, prefix)
	if err != nil {
		return err
	}

	slices.SortStableFunc(objs, func(a, b Object) int {
		return cmp.Or(b.Modified.Compare(a.Modified), b.Time.Compare(a.Time))
	})

	var errs []error

	kept := 1 // justWritten

	for _, o := range objs {
		if o.Key == justWritten {
			continue
		}

		if kept < keep {
			kept++

			continue
		}

		errs = append(errs, s.Delete(ctx, o.Key))
	}

	return errors.Join(errs...)
}
