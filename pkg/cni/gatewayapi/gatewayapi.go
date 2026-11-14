// Package gatewayapi embeds the Gateway API CRDs that Cilium's gatewayAPI needs. Cilium's chart does not ship
// them, so `ichor init --gateway-api` applies them through the API server once it answers.
package gatewayapi

import _ "embed" // the extracted CRDs, from make gateway-api-crds

//go:embed crds.yaml
var crds []byte

// CRDs returns the seven CRDs as one multi-document YAML stream.
func CRDs() []byte { return crds }
