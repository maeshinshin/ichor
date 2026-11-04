package oidcissuer

import (
	"errors"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// internalAudience is the aud of codes, tickets and refresh tokens. No consumer
// accepts it, so none of them is usable as an ID token. The JWS "typ" values
// (ichorauth.Typ*) keep the three kinds from being replayed as each other.
const internalAudience = "ichor-oidc-internal"

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
	// Family ties a token to its rotation chain; empty on a token issued before rotation existed.
	Family string `json:"fam,omitempty"`
}

type idClaims struct {
	Groups []string `json:"groups"`
	Nonce  string   `json:"nonce,omitempty"`
}

// sign serializes std plus private claims as a compact JWS signed with the
// current signing key. typ "JWT" is used for ID tokens.
func (i *Issuer) sign(typ string, std jwt.Claims, private ...any) (string, error) {
	key, err := i.cfg.Keys.Signing()
	if err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType(jose.ContentType(typ)))
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
	// A compact JWS has exactly one header. The key set lookup by kid rejects a
	// missing or unknown kid.
	if err != nil {
		return std, errBadToken
	}
	if got, _ := t.Headers[0].ExtraHeaders[jose.HeaderType].(string); got != typ {
		return std, errBadToken
	}
	keys := jose.JSONWebKeySet{Keys: i.cfg.Keys.Public()}
	if err := t.Claims(keys, append([]any{&std}, private...)...); err != nil {
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
