// Package spec supplies the shipped, LLM-readable product specification.
package spec

import _ "embed"

//go:embed requirements.md
var Requirements string

//go:embed architecture.md
var Architecture string
