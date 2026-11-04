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

package ichorauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Names fixed by ADR 0094.
const (
	// CredentialsSecretName is the one Secret (in ichor-system) holding every user's hash.
	CredentialsSecretName = "ichor-user-credentials"
	// SigningSecretName holds the issuer's signing keys; the controller writes it.
	SigningSecretName = "ichor-oidc-signing"

	// UserPrefix is the Kubernetes username prefix of an Ichor user (ichor:<name>); GroupPrefix
	// is the group prefix (ichor:<role>). ExternalPrefix is the default for an external IdP.
	UserPrefix     = "ichor:"
	GroupPrefix    = "ichor:"
	ExternalPrefix = "oidc:"
	// SystemPrefix is blocked for users and groups on every issuer.
	SystemPrefix = "system:"

	// Built-in role names; they are reserved, and map to the groups below.
	RoleViewer   = "viewer"
	RoleOperator = "operator"
	RoleAdmin    = "admin"

	GroupViewer   = GroupPrefix + RoleViewer
	GroupOperator = GroupPrefix + RoleOperator
	GroupAdmin    = GroupPrefix + RoleAdmin

	// ClientUI and ClientCLI are the two fixed public OAuth clients.
	ClientUI  = "ichor-ui"
	ClientCLI = "ichor-cli"

	// AudienceUI is the audience of tokens for ichor-ui; AudienceKube is the only audience the
	// kube-apiserver trusts.
	AudienceUI   = "ichor-ui"
	AudienceKube = "ichor-kube"

	// JWS typ header values, so a code cannot be replayed as a refresh token or a ticket.
	TypCode    = "ichor-code+jwt"
	TypRefresh = "ichor-refresh+jwt"
	TypTicket  = "ichor-ticket+jwt"

	// SigningAlgorithm is the only algorithm the issuer uses.
	SigningAlgorithm = "RS256"
)

// Lifetimes: defaults and bounds of the chart values (ADR 0094 "TTLs").
const (
	DefaultCodeTTL         = 60 * time.Second
	DefaultTicketTTL       = 5 * time.Minute
	DefaultTokenTTL        = 5 * time.Minute
	MinTokenTTL            = 1 * time.Minute
	MaxTokenTTL            = time.Hour
	DefaultRefreshLifetime = 12 * time.Hour
	MinRefreshLifetime     = 1 * time.Hour
	MaxRefreshLifetime     = 30 * 24 * time.Hour
)

// MaxRoles is the most roles one user can have.
const MaxRoles = 16

var (
	userNameRE = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)
	roleRE     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
)

// ValidateUserName checks the IchorUser name rules: a lower-case DNS label of at most 63
// characters (the CRD enforces the same with CEL).
func ValidateUserName(name string) error {
	if len(name) > 63 || !userNameRE.MatchString(name) {
		return fmt.Errorf("user name must be a lower-case DNS label of at most 63 characters")
	}
	return nil
}

// ValidateRole checks one role name (the CRD enforces the same pattern).
func ValidateRole(role string) error {
	if !roleRE.MatchString(role) {
		return fmt.Errorf("role %q must match %s", role, roleRE)
	}
	return nil
}

// ValidateRoles checks a user's whole role list: at most MaxRoles, each a valid role name.
func ValidateRoles(roles []string) error {
	if len(roles) > MaxRoles {
		return fmt.Errorf("at most %d roles", MaxRoles)
	}
	errs := make([]error, len(roles))
	for i, r := range roles {
		errs[i] = ValidateRole(r)
	}
	return errors.Join(errs...)
}

// IsBuiltinRole reports whether the role is one of the reserved built-in names.
func IsBuiltinRole(role string) bool {
	return role == RoleViewer || role == RoleOperator || role == RoleAdmin
}

// KubeUser is the Kubernetes username the apiserver derives for an Ichor user.
func KubeUser(name string) string { return UserPrefix + name }

// KubeGroups maps roles to the Kubernetes groups the token's groups claim yields after the
// apiserver adds its prefix; the claim itself carries the bare role names (see GroupsClaim).
func KubeGroups(roles []string) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = GroupPrefix + r
	}
	return out
}

// HasSystemPrefix reports whether a user or group name starts with the blocked system: prefix.
func HasSystemPrefix(s string) bool { return strings.HasPrefix(s, SystemPrefix) }

// ValidateExternalPrefix is the webhook rule for an external IdP's prefix: it must not be empty,
// and must not equal or start with the Ichor prefix, so the two sources cannot collide.
func ValidateExternalPrefix(prefix string) error {
	if prefix == "" {
		return errors.New("external prefix must not be empty")
	}
	if strings.HasPrefix(prefix, UserPrefix) {
		return fmt.Errorf("external prefix %q must not equal or start with %q", prefix, UserPrefix)
	}
	return nil
}

// CredentialVersion is the current schema version of Credential.
const CredentialVersion = 1

// Credential is one user's entry in the credentials Secret (key = user name, value = this as
// JSON). It is secret material (the hash), so it has no String method that prints it and must
// never be logged.
type Credential struct {
	Version    int       `json:"v"`
	Hash       string    `json:"hash"`
	NotBefore  time.Time `json:"notBefore"`
	MustChange bool      `json:"mustChange"`
	// Families are the user's live refresh-token families by id (docs/adr/0108): the id of the
	// family's current token and when the family dies. A retired token's reuse deletes the family.
	Families map[string]RefreshFamily `json:"families,omitempty"`
}

// RefreshFamily is the state of one rotating chain of refresh tokens.
type RefreshFamily struct {
	JTI string `json:"jti"`
	Exp int64  `json:"exp"`
}

// MaxRefreshFamilies caps the live families of one user; the oldest-expiring is dropped first.
const MaxRefreshFamilies = 16

// NewCredential hashes the password (policy applied) into an entry whose NotBefore is now, which
// invalidates every refresh token issued earlier. NotBefore is truncated to whole seconds because
// token iat claims are whole seconds; a token issued in the same second as the change stays valid.
func NewCredential(password string, mustChange bool, now time.Time) (Credential, error) {
	h, err := Hash(password)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		Version: CredentialVersion, Hash: h, NotBefore: now.UTC().Truncate(time.Second), MustChange: mustChange,
	}, nil
}

// Marshal is the Secret value.
func (c Credential) Marshal() ([]byte, error) { return json.Marshal(c) }

// MergePatch is a JSON merge patch that sets only this user's key in the credentials Secret, so
// two writers never overwrite each other's users.
func (c Credential) MergePatch(name string) ([]byte, error) {
	raw, err := c.Marshal()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"data": map[string][]byte{name: raw}})
}

// ParseCredential reads a Secret value. The error never includes the value.
func ParseCredential(data []byte) (Credential, error) {
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return Credential{}, errors.New("credential entry is not valid JSON")
	}
	if c.Version != CredentialVersion {
		return Credential{}, fmt.Errorf("unsupported credential version %d", c.Version)
	}
	if c.Hash == "" {
		return Credential{}, errors.New("credential entry has no hash")
	}
	return c, nil
}

// Verify checks the password against the entry's hash; see the package-level Verify.
func (c Credential) Verify(password string) (match, needsRehash bool, err error) {
	return Verify(password, c.Hash)
}

// SessionValid reports whether a session that started at iat is still allowed by NotBefore.
func (c Credential) SessionValid(iat time.Time) bool { return !iat.Before(c.NotBefore) }
