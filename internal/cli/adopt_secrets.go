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
	"io/fs"
	"path/filepath"

	yaml "go.yaml.in/yaml/v4"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// adoptSecretsOptions are the flags that decide where adopt gets the
// cluster's secrets bundle. Exactly one source is chosen.
type adoptSecretsOptions struct {
	// File is --secrets-bundle: a `talosctl gen secrets` file.
	File string
	// Recover is --recover-secrets: rebuild the bundle from a control plane's machine config.
	Recover bool
	// RecoverFrom is the control plane address the config is read from.
	RecoverFrom string
	// Cluster names ~/.ichor/clusters/<Cluster>/, where a recovered bundle is stored.
	Cluster string
	// Contract is the cluster's version contract ("vX.Y"); empty means the machinery's current one.
	Contract string
	// Plaintext is --bundle-plaintext: store a recovered bundle unencrypted.
	Plaintext bool
	// Announce receives a generated bundle passphrase and the plaintext warning; nil means stderr. It is
	// separate because stderr may be the progress view, which would rewrite the lines.
	Announce io.Writer
}

// errNoSecretsSource and friends are the flag-combination refusals. They
// never carry bundle material.
var (
	errNoSecretsSource   = errors.New("adopt: give either --secrets-bundle FILE or --recover-secrets")
	errBothSecretsSource = errors.New("adopt: --secrets-bundle and --recover-secrets are mutually exclusive")
	errNoRecoverFrom     = errors.New("adopt: --recover-secrets needs --recover-from ADDRESS, the address of one control plane")
	errNotControlPlane   = errors.New("adopt: the machine config read is not a control plane's, so it holds no cluster secrets; point --recover-from at a control plane")
	errRecoveredExists   = errors.New("adopt: a secrets bundle already exists for this cluster; refusing to overwrite it, pass it with --secrets-bundle or move it away")
)

// resolveAdoptBundle returns the secrets bundle adopt uses: the given file,
// or one recovered read-only from a control plane. A recovered bundle is held
// in memory only: the second result stores it 0600 under
// ~/.ichor/clusters/<cluster>/ and prints the path through nagBackup (the
// bundle itself is never printed). It is nil when a file was given. The plan
// phase writes nothing, so the caller saves after consent.
func resolveAdoptBundle(
	ctx context.Context, opts adoptSecretsOptions,
	dial func(ctx context.Context, endpoint string) (talosConfigReader, error),
	stderr io.Writer,
) (*secrets.Bundle, func() error, error) {
	switch {
	case opts.File != "" && opts.Recover:
		return nil, nil, errBothSecretsSource
	case opts.File != "":
		bundle, err := loadBundleFile(opts.File)

		return bundle, nil, err
	case !opts.Recover:
		return nil, nil, errNoSecretsSource
	case opts.RecoverFrom == "":
		return nil, nil, errNoRecoverFrom
	}

	dir, err := bundleDir(opts.Cluster)
	if err != nil {
		return nil, nil, err
	}

	target := filepath.Join(dir, secretsbundle.SecretKey)

	contract, err := parseAdoptContract(opts.Contract)
	if err != nil {
		return nil, nil, err
	}

	talos, err := dial(ctx, opts.RecoverFrom)
	if err != nil {
		return nil, nil, fmt.Errorf("adopt: dialing %q to recover the secrets bundle: %w", opts.RecoverFrom, err)
	}

	if closer, ok := talos.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	bundle, err := recoverSecretsBundle(ctx, talos, contract)
	if err != nil {
		return nil, nil, err
	}

	// A bundle stored by an earlier run (killed after it saved) is fine when this recovery
	// reproduces it exactly; a different one is never overwritten.
	switch stored, _, err := readBundlePlain(target); {
	case err == nil:
		again, mErr := yaml.Marshal(bundle)
		if mErr != nil || !bytes.Equal(stored, again) {
			return nil, nil, errRecoveredExists
		}

		return bundle, func() error { return nil }, nil
	case !errors.Is(err, fs.ErrNotExist):
		// An encrypted file that cannot be opened: say so rather than overwrite or ignore it.
		return nil, nil, err
	}

	return bundle, func() error {
		announce := opts.Announce
		if announce == nil {
			announce = stderr
		}

		path, err := saveSealedBundle(opts.Cluster, bundle, opts.Plaintext, false, announce)
		if err != nil {
			return err
		}

		nagBackup(stderr, path)

		return nil
	}, nil
}

func parseAdoptContract(v string) (*coreconfig.VersionContract, error) {
	if v == "" {
		return coreconfig.TalosVersionCurrent, nil
	}

	c, err := coreconfig.ParseContractFromVersion(v)
	if err != nil {
		return nil, fmt.Errorf("adopt: parsing version contract %q: %w", v, err)
	}

	return c, nil
}

// recoverSecretsBundle reads one control plane's live machine config and
// rebuilds the cluster's secrets bundle from it with
// secrets.NewBundleFromConfig, then validates it against contract. It is
// read-only on the node.
//
// Errors name structure only: a loader or machinery error is never wrapped
// with %w, because it could quote config text; and the bundle is never in
// a message. A wrong or partial bundle that still validates is caught
// later by the zero-drift diff.
func recoverSecretsBundle(
	ctx context.Context, talos talosConfigReader, contract *coreconfig.VersionContract,
) (*secrets.Bundle, error) {
	raw, err := talos.CurrentConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("adopt: reading the control plane's machine config: %w", err)
	}

	cfg, err := configloader.NewFromBytes(raw)
	if err != nil {
		return nil, errors.New("adopt: the control plane's machine config could not be parsed")
	}

	// NewBundleFromConfig dereferences these for a control plane; a worker config lacks them.
	if cfg.Machine() == nil || !cfg.Machine().Type().IsControlPlane() || cfg.Cluster() == nil || cfg.Cluster().Token() == nil {
		return nil, errNotControlPlane
	}

	bundle, err := secrets.NewBundleFromConfig(secrets.NewClock(), cfg)
	if err != nil {
		return nil, errors.New("adopt: rebuilding the secrets bundle from the machine config failed")
	}

	if err := bundle.Validate(contract); err != nil {
		// Validate's messages are field names ("certs.k8s is required") or certificate parse
		// errors; neither carries key material.
		return nil, fmt.Errorf("adopt: the recovered secrets bundle is incomplete or invalid: %w", err)
	}

	return bundle, nil
}

// clusterVersionContract returns the TalosCluster's spec.versionContract,
// or "" when the object does not exist (yet) or cannot be read; "" selects
// the machinery's current contract in parseAdoptContract.
func clusterVersionContract(ctx context.Context, k8s client.Client, namespace, name string) string {
	var cluster ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cluster); err != nil {
		return ""
	}

	return cluster.Spec.VersionContract
}
