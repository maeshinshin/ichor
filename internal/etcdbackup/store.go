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
	Key  string // full key, prefix included
	Name string // base name
	Time time.Time
	Size int64
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
		return nil, notFound(err)
	}

	// GetObject is lazy: surface a missing key now rather than on first Read.
	if _, err := o.Stat(); err != nil {
		_ = o.Close()

		return nil, notFound(err)
	}

	return o, nil
}

// notFound wraps a missing-key answer from S3 in ErrNotFound; any other error is returned as it is.
func notFound(err error) error {
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
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
			out = append(out, Object{Key: o.Key, Name: name, Time: t, Size: o.Size})
		}
	}

	slices.SortFunc(out, func(a, b Object) int { return b.Time.Compare(a.Time) })

	return out, nil
}

// Prune deletes all but the newest keep snapshots under prefix. It is only
// ever called after a successful upload, so the newest snapshot always
// survives; a failed delete is returned after trying the rest.
func Prune(ctx context.Context, s Store, prefix string, keep int) error {
	objs, err := s.List(ctx, prefix)
	if err != nil {
		return err
	}

	var errs []error

	for _, o := range objs[min(keep, len(objs)):] {
		errs = append(errs, s.Delete(ctx, o.Key))
	}

	return errors.Join(errs...)
}
