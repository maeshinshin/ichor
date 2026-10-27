package oidcissuer

import (
	"crypto/rand"
	"encoding/base64"
	"errors"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// JWS "typ" values keep the three internal token kinds (and ID tokens, which
// carry plain "JWT") from being replayed as each other.
const (
	typCode    = "ichor-code+jwt"
	typTicket  = "ichor-ticket+jwt"
	typRefresh = "ichor-refresh+jwt"

	// internalAudience is the aud of codes, tickets and refresh tokens. No
	// consumer accepts it, so none of them is usable as an ID token.
	internalAudience = "ichor-oidc-internal"
)

// authRequest is the part of an authorize request carried in a ticket and then
// in a code.
type authRequest struct {
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	Scope         string `json:"scope"`
	State         string `json:"state,omitempty"` // ticket only
	Nonce         string `json:"nonce"`
	CodeChallenge string `json:"code_challenge"`
	TokenAudience string `json:"token_aud"`
}

type refreshClaims struct {
	ClientID      string `json:"client_id"`
	Scope         string `json:"scope"`
	TokenAudience string `json:"token_aud"`
}

type idClaims struct {
	Groups []string `json:"groups"`
	Nonce  string   `json:"nonce,omitempty"`
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sign serializes std plus private claims as a compact JWS signed with the
// current signing key. typ "JWT" is used for ID tokens.
func (i *Issuer) sign(typ string, std jwt.Claims, private ...any) (string, error) {
	key, err := i.cfg.Keys.Signing()
	if err != nil {
		return "", err
	}
	opts := (&jose.SignerOptions{}).WithType(jose.ContentType(typ))
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, opts)
	if err != nil {
		return "", err
	}
	b := jwt.Signed(signer).Claims(std)
	for _, p := range private {
		b = b.Claims(p)
	}
	return b.Serialize()
}

var errBadToken = errors.New("invalid token")

// verify checks signature (against every published key), typ, issuer, audience
// and expiry of one of our own tokens and decodes the private claims.
func (i *Issuer) verify(token, typ string, private ...any) (jwt.Claims, error) {
	var std jwt.Claims
	t, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(t.Headers) != 1 {
		return std, errBadToken
	}
	h := t.Headers[0]
	if got, _ := h.ExtraHeaders[jose.HeaderType].(string); got != typ || h.KeyID == "" {
		return std, errBadToken
	}
	var key *jose.JSONWebKey
	for _, k := range i.cfg.Keys.Public() {
		if k.KeyID == h.KeyID {
			key = &k
			break
		}
	}
	if key == nil {
		return std, errBadToken
	}
	if err := t.Claims(key, append([]any{&std}, private...)...); err != nil {
		return std, errBadToken
	}
	err = std.ValidateWithLeeway(jwt.Expected{
		Issuer:      i.cfg.Issuer,
		AnyAudience: jwt.Audience{internalAudience},
		Time:        i.cfg.Now(),
	}, 0)
	if err != nil || std.Expiry == nil || std.IssuedAt == nil {
		return std, errBadToken
	}
	return std, nil
}
