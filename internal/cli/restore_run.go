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

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

type restoreOptions struct {
	Cluster       string
	SecretsBundle string
	NodeEndpoint  string
	Snapshot      string
	S3            etcdbackup.S3Config // Endpoint, Bucket, Region only; credentials come from the environment
	S3Prefix      string
	S3CAFile      string
}

// recoverClient is the two Talos calls full-loss recovery makes, in this
// order, on one node: upload the snapshot, then bootstrap etcd
// from it.
type recoverClient interface {
	EtcdRecover(ctx context.Context, snapshot io.Reader) error
	BootstrapRecover(ctx context.Context) error
}

type restoreDeps struct {
	NewStore func(etcdbackup.S3Config) (etcdbackup.Store, error)
	Dial     func(ctx context.Context, cluster, endpoint string, bundle *secrets.Bundle) (recoverClient, error)
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)

	Stdout, Stderr io.Writer
	Stdin          io.Reader

	AssumeYes bool
}

// runRecoverCluster is `ichor restore cluster`. Every
// step that can fail without touching the node (bundle, listing, download,
// decrypt, integrity check) runs before the confirmation, so the operator
// confirms a snapshot already known good, and nothing is sent otherwise.
func runRecoverCluster(ctx context.Context, opts restoreOptions, deps restoreDeps) error {
	for _, req := range []struct{ flag, value string }{
		{"--cluster", opts.Cluster}, {"--node-endpoint", opts.NodeEndpoint}, {"--s3-endpoint", opts.S3.Endpoint},
		{"--s3-bucket", opts.S3.Bucket}, {"--s3-prefix", opts.S3Prefix},
	} {
		if req.value == "" {
			return fmt.Errorf("restore: %s is required", req.flag)
		}
	}

	p := newProgress(deps.Stderr)
	p.Header("Recover etcd", fmt.Sprintf("cluster %s · node %s", opts.Cluster, opts.NodeEndpoint))

	var in *recoveryInputs

	if err := p.Run("Check inputs", func(*step) (string, error) {
		var err error

		in, err = openRecoveryInputs(opts, deps)
		if err != nil {
			return "", err
		}

		return in.bundlePath, nil
	}); err != nil {
		return err
	}

	var snap *verifiedSnapshot

	if err := p.Run("Verify snapshot", func(*step) (string, error) {
		var err error

		snap, err = pickSnapshot(ctx, in.store, opts, in.passphrase, p.WarnWriter(), in.scrub)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("%s (taken %s, %s, verified)", snap.name, snap.time.Format("2006-01-02 15:04:05 UTC"), byteSize(int64(snap.data.Len()))), nil
	}); err != nil {
		return err
	}

	if !deps.AssumeYes {
		if err := confirmOrAbort(deps.Stdin, deps.Stderr, "restore", fmt.Sprintf(
			"Recover etcd on %s from snapshot %s (%s, %d bytes, verified)?\n"+
				"The cluster's state reverts to that time. The node's etcd data directory must be empty. "+
				"This cannot be undone. [y/N]: ",
			opts.NodeEndpoint, snap.name, snap.time.Format("2006-01-02 15:04:05 UTC"), snap.data.Len()),
			"aborted before contacting the node; nothing was changed"); err != nil {
			return err
		}
	}

	if err := recoverOnNode(ctx, p, opts, deps, in, snap); err != nil {
		return err
	}

	h := newHuman(deps.Stdout)
	h.Note(h.Mark(markOK) + " etcd on " + opts.NodeEndpoint + " is recovering from " + snap.name)
	h.Note("Next: once the Kubernetes API is back, the restored Ichor controller re-reconciles the cluster; " +
		"check each node before approving anything.")

	return h.Err()
}

// recoveryInputs is what the recovery needs before it touches the node.
type recoveryInputs struct {
	bundlePath string
	bundle     *secrets.Bundle
	passphrase etcdbackup.Key
	store      etcdbackup.Store
	// scrub rewrites an error with every secret held here replaced.
	scrub func(error) error
}

// openRecoveryInputs reads the secrets bundle, derives the snapshot key, takes the S3 credentials
// from the environment and opens the store.
func openRecoveryInputs(opts restoreOptions, deps restoreDeps) (*recoveryInputs, error) {
	bundlePath := opts.SecretsBundle
	if bundlePath == "" {
		dir, err := bundleDir(opts.Cluster)
		if err != nil {
			return nil, fmt.Errorf("restore: %w", err)
		}

		bundlePath = filepath.Join(dir, secretsbundle.SecretKey)
	}

	bundle, err := loadBundleFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}

	// The keys a key rotation retired sit next to the bundle; without them a backup sealed before it is unreadable.
	retired, err := loadRetiredFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}

	passphrase, err := etcdbackup.DeriveKey(bundle, retired.Secretboxes()...)
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}

	s3 := opts.S3
	s3.AccessKeyID = firstEnv(deps.Getenv, "ICHOR_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	s3.SecretAccessKey = firstEnv(deps.Getenv, "ICHOR_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	s3.SessionToken = firstEnv(deps.Getenv, "ICHOR_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")

	if s3.AccessKeyID == "" || s3.SecretAccessKey == "" {
		return nil, errors.New("restore: set ICHOR_S3_ACCESS_KEY_ID and ICHOR_S3_SECRET_ACCESS_KEY (they are not accepted as flags)")
	}

	if opts.S3CAFile != "" {
		if s3.CAPEM, err = deps.ReadFile(opts.S3CAFile); err != nil {
			return nil, fmt.Errorf("restore: reading --s3-ca-file: %w", err)
		}
	}

	// Errors whose text comes from S3, TLS or gRPC are scrubbed of every secret held here.
	scrub := newScrubber(append(append(passphrase.Secrets(), retired.Secretboxes()...), s3.AccessKeyID, s3.SecretAccessKey, s3.SessionToken)...)

	store, err := deps.NewStore(s3)
	if err != nil {
		return nil, fmt.Errorf("restore: %w", scrub(err))
	}

	return &recoveryInputs{bundlePath: bundlePath, bundle: bundle, passphrase: passphrase, store: store, scrub: scrub}, nil
}

// recoverOnNode uploads the snapshot to the node and bootstraps etcd from it.
func recoverOnNode(ctx context.Context, p *progress, opts restoreOptions, deps restoreDeps, in *recoveryInputs, snap *verifiedSnapshot) error {
	var c recoverClient

	if err := p.Run("Connect", func(*step) (string, error) {
		var err error

		if c, err = deps.Dial(ctx, opts.Cluster, opts.NodeEndpoint, in.bundle); err != nil {
			return "", fmt.Errorf("restore: %w", in.scrub(err))
		}

		return opts.NodeEndpoint, nil
	}); err != nil {
		return err
	}

	if closer, ok := c.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	if err := p.Run("Upload snapshot", func(*step) (string, error) {
		if err := c.EtcdRecover(ctx, bytes.NewReader(snap.data.Bytes())); err != nil {
			return "", fmt.Errorf("restore: uploading the snapshot to %q: %w", opts.NodeEndpoint, in.scrub(err))
		}

		return byteSize(int64(snap.data.Len())), nil
	}); err != nil {
		return err
	}

	return p.Run("Bootstrap etcd", func(*step) (string, error) {
		if err := c.BootstrapRecover(ctx); err != nil {
			return "", fmt.Errorf("restore: bootstrapping etcd from the snapshot on %q: %w", opts.NodeEndpoint, in.scrub(err))
		}

		return "from the snapshot", nil
	})
}

type verifiedSnapshot struct {
	name string
	time time.Time
	data *bytes.Buffer // decrypted and integrity-checked: exactly what EtcdSnapshot streamed
}

// pickSnapshot downloads, decrypts and verifies snapshots newest first and
// returns the first good one. A named snapshot is the only candidate; a
// broken one is an error, never replaced by another.
func pickSnapshot(ctx context.Context, store etcdbackup.Store, opts restoreOptions, passphrase etcdbackup.Key, stderr io.Writer, scrub func(error) error) (*verifiedSnapshot, error) {
	objs, err := store.List(ctx, opts.S3Prefix)
	if err != nil {
		return nil, fmt.Errorf("restore: listing snapshots under %q: %w", opts.S3Prefix, scrub(err))
	}

	if opts.Snapshot != "" {
		var named []etcdbackup.Object

		for _, o := range objs {
			if o.Name == opts.Snapshot {
				named = append(named, o)
			}
		}

		objs = named
	}

	if len(objs) == 0 {
		return nil, fmt.Errorf("restore: no matching snapshot under prefix %q in the bucket", opts.S3Prefix)
	}

	var problems []string

	for _, o := range objs {
		buf, err := etcdbackup.FetchVerified(ctx, store, o.Key, passphrase)
		if err != nil {
			err = scrub(err)
			problems = append(problems, fmt.Sprintf("  %s: %v", o.Name, err))
			_, _ = fmt.Fprintf(stderr, "skipping %s: %v\n", o.Name, err)

			continue
		}

		return &verifiedSnapshot{name: o.Name, time: o.Time, data: buf}, nil
	}

	return nil, fmt.Errorf("restore: no usable snapshot (nothing was sent to any node):\n%s", strings.Join(problems, "\n"))
}

func firstEnv(getenv func(string) string, names ...string) string {
	for _, n := range names {
		if v := getenv(n); v != "" {
			return v
		}
	}

	return ""
}

// newScrubber returns a function that rewrites an error with every non-empty
// secret replaced.
func newScrubber(secretValues ...string) func(error) error {
	var pairs []string

	for _, s := range secretValues {
		if s != "" {
			pairs = append(pairs, s, "[redacted]")
		}
	}

	r := strings.NewReplacer(pairs...)

	return func(err error) error {
		if err == nil || len(pairs) == 0 {
			return err
		}

		return errors.New(r.Replace(err.Error()))
	}
}

// dialRecover connects to one control plane as the bundle's admin identity,
// the recipe dialAdmin uses for init.
func dialRecover(ctx context.Context, cluster, endpoint string, bundle *secrets.Bundle) (recoverClient, error) {
	cert, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Admin))
	if err != nil {
		return nil, fmt.Errorf("generating admin client certificate: %w", err)
	}

	cfg := clientconfig.NewConfig(cluster, []string{endpoint}, bundle.Certs.OS.Crt, cert)

	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dialing Talos API at %q: %w", endpoint, err)
	}

	return &recoverTalos{c: c}, nil
}

type recoverTalos struct{ c *client.Client }

func (r *recoverTalos) Close() error { return r.c.Close() }

func (r *recoverTalos) EtcdRecover(ctx context.Context, snapshot io.Reader) error {
	_, err := r.c.EtcdRecover(ctx, snapshot)

	return err
}

func (r *recoverTalos) BootstrapRecover(ctx context.Context) error {
	return r.c.Bootstrap(ctx, &machineapi.BootstrapRequest{RecoverEtcd: true})
}
