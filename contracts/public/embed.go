// Package public embeds the canonical transport contracts in shipped binaries.
package public

import _ "embed"

//go:embed schema.json
var Schema []byte

//go:embed openapi.json
var OpenAPI []byte
