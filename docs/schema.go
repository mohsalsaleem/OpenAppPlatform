// Package docs bundles the versioned public API schema for machine clients.
package docs

import _ "embed"

//go:embed openapi.json
var OpenAPI []byte
