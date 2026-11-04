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
	"context"
	"crypto/sha256"
	stdx509 "crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/cosi-project/runtime/pkg/safe"
	secretsres "github.com/siderolabs/talos/pkg/machinery/resources/secrets"
)

// EtcdPKI is the part of a node's etcd PKI a CA rotation checks before it restarts the member. It holds public data
// only (fingerprints), never a key.
type EtcdPKI struct {
	// CAs are the SHA-256 fingerprints (hex, of the DER) of the certificates in the node's etcd CA
	// field, in order. Talos writes the field verbatim into the file etcd and kube-apiserver trust.
	CAs []string
	// ServerSignedBy is the fingerprint, among CAs, of the certificate that signed the node's etcd server
	// certificate; empty when none of them did.
	ServerSignedBy string
}

// etcdPKIFrom computes an EtcdPKI from the PEM of the etcd CA field and of the node's etcd server certificate.
func etcdPKIFrom(caPEM, serverPEM []byte) (EtcdPKI, error) {
	var (
		out  EtcdPKI
		cas  []*stdx509.Certificate
		rest = caPEM
	)

	for {
		var block *pem.Block

		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}

		cert, err := stdx509.ParseCertificate(block.Bytes)
		if err != nil {
			return EtcdPKI{}, errors.New("a certificate in the etcd CA field cannot be parsed")
		}

		sum := sha256.Sum256(block.Bytes)
		out.CAs = append(out.CAs, hex.EncodeToString(sum[:]))
		cas = append(cas, cert)
	}

	if len(cas) == 0 {
		return EtcdPKI{}, errors.New("the etcd CA field holds no certificate")
	}

	block, _ := pem.Decode(serverPEM)
	if block == nil {
		return EtcdPKI{}, errors.New("the etcd server certificate cannot be read")
	}

	server, err := stdx509.ParseCertificate(block.Bytes)
	if err != nil {
		return EtcdPKI{}, errors.New("the etcd server certificate cannot be parsed")
	}

	for i, ca := range cas {
		if server.CheckSignatureFrom(ca) == nil {
			out.ServerSignedBy = out.CAs[i]

			break
		}
	}

	return out, nil
}

// EtcdPKI reads the etcd root and the generated server certificate from the node's COSI state (the
// secrets namespace, which the admin role may read) and reports their fingerprints.
func (t *machineryTalosClient) EtcdPKI(ctx context.Context) (EtcdPKI, error) {
	root, err := safe.StateGetByID[*secretsres.EtcdRoot](ctx, t.c.COSI, secretsres.EtcdRootID)
	if err != nil {
		return EtcdPKI{}, fmt.Errorf("reading the etcd root secret: %w", err)
	}

	certs, err := safe.StateGetByID[*secretsres.Etcd](ctx, t.c.COSI, secretsres.EtcdID)
	if err != nil {
		return EtcdPKI{}, fmt.Errorf("reading the etcd certificates: %w", err)
	}

	ca, server := root.TypedSpec().EtcdCA, certs.TypedSpec().Etcd
	if ca == nil || server == nil {
		return EtcdPKI{}, errors.New("the node has no etcd CA or etcd server certificate yet")
	}

	return etcdPKIFrom(ca.Crt, server.Crt)
}
