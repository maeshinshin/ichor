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
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/client"
)

// A node in maintenance mode serves a self-signed certificate nobody can verify, and the first apply
// sends it the whole machine configuration, CA keys included. What can be checked is that the node that
// answers later is the node that answered before: a MaintenancePin remembers the SPKI fingerprint of
// the certificate seen at the first handshake and refuses any later handshake that presents another
// one. It does not authenticate the first sight; provisioning still assumes a separate
// network. Talos issues a new maintenance certificate when the node reboots or changes address, so a
// refusal after either is expected and the operator accepts the new fingerprint explicitly
// (ichorv1alpha1.AnnotationTrustMaintenanceFingerprint).

// maintenancePinMismatch is in the error of a handshake refused by the pin; it survives gRPC's wrapping,
// which is how the caller tells it from an unreachable node.
const maintenancePinMismatch = "maintenance certificate fingerprint changed"

// IsMaintenancePinMismatch reports whether err is a handshake the pin refused.
func IsMaintenancePinMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), maintenancePinMismatch)
}

// MaintenancePin is shared by every connection made with the same context (WithMaintenancePin), so
// the identity read and the apply of one provisioning run are held to the same certificate.
type MaintenancePin struct {
	mu   sync.Mutex
	want x509.Fingerprint
	seen x509.Fingerprint
}

// NewMaintenancePin pins to want (an x509.Fingerprint string); empty trusts the first handshake seen.
func NewMaintenancePin(want string) (*MaintenancePin, error) {
	p := &MaintenancePin{}

	if want != "" {
		fp, err := x509.ParseFingerprint(want)
		if err != nil {
			return nil, fmt.Errorf("parsing the pinned maintenance fingerprint: %w", err)
		}

		p.want = fp
	}

	return p, nil
}

// Seen is the fingerprint of the certificate the node presented, empty before the first handshake.
func (p *MaintenancePin) Seen() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.seen == nil {
		return ""
	}

	return p.seen.String()
}

func (p *MaintenancePin) verify(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("the node presented no certificate")
	}

	fp := x509.SPKIFingerprint(cs.PeerCertificates[0])

	p.mu.Lock()
	defer p.mu.Unlock()

	switch {
	case p.want != nil && !p.want.Equal(fp):
		p.seen = fp

		return fmt.Errorf("%s: the node presented %s, pinned %s", maintenancePinMismatch, fp, p.want)
	case p.want == nil:
		p.want = fp
	}

	p.seen = fp

	return nil
}

// ClientOptions dial endpoint in maintenance mode, held to the pin.
func (p *MaintenancePin) ClientOptions(endpoint string) []client.OptionFunc {
	return []client.OptionFunc{
		client.WithMaintenanceMode(endpoint, nil),
		client.WithTLSConfig(&tls.Config{InsecureSkipVerify: true, VerifyConnection: p.verify}), //nolint:gosec // the node's certificate is self-signed; verify pins its fingerprint
	}
}

type maintenancePinKey struct{}

// WithMaintenancePin makes every maintenance dial made with the returned context use p.
func WithMaintenancePin(ctx context.Context, p *MaintenancePin) context.Context {
	return context.WithValue(ctx, maintenancePinKey{}, p)
}

// PinFromContext is the pin of ctx, or a fresh trust-on-first-use pin when it has none.
func PinFromContext(ctx context.Context) *MaintenancePin {
	if p, ok := ctx.Value(maintenancePinKey{}).(*MaintenancePin); ok && p != nil {
		return p
	}

	return &MaintenancePin{}
}

// PeerFingerprinter is implemented by the maintenance clients that record the certificate they saw.
type PeerFingerprinter interface{ PeerFingerprint() string }
