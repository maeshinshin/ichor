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

// Package reclaim returns a machine that already runs a configured Talos to
// maintenance mode so that Ichor can join it (ADR 0103, section 2).
//
// It is shared by the CLI (`ichor machine reclaim`) and, later, the
// controller's MachineReclaim path. Both inspect the node the same way, apply
// the same refusals, require the same typed UUID and send the same reset
// request (ResetRequest, ADR 0042).
//
// The credential is the operator's own foreign talosconfig. It is held in a
// Credential, which never prints, and is never stored by this package.
package reclaim

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// redacted is what a Credential prints as.
const redacted = "[REDACTED talosconfig]"

// Credential is the bytes of a foreign talosconfig. Every way of printing it
// (%v, %+v, %#v, %s, JSON, slog) yields a fixed placeholder; only Bytes
// returns the content, and the only caller of that in this package is Dial.
type Credential struct {
	b []byte
}

// NewCredential wraps the talosconfig bytes b. The slice is not copied.
func NewCredential(b []byte) Credential { return Credential{b: b} }

// Bytes returns the talosconfig content. Do not log or print the result.
func (c Credential) Bytes() []byte { return c.b }

// Empty reports whether the credential holds nothing.
func (c Credential) Empty() bool { return len(c.b) == 0 }

// String implements fmt.Stringer.
func (Credential) String() string { return redacted }

// Format implements fmt.Formatter so that no verb can reach the bytes.
func (Credential) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(redacted)) }

// MarshalJSON implements json.Marshaler.
func (Credential) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// MarshalText implements encoding.TextMarshaler (YAML encoders use it).
func (Credential) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue implements slog.LogValuer.
func (Credential) LogValue() slog.Value { return slog.StringValue(redacted) }
