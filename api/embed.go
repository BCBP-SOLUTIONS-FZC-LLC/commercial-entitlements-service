// Package api embeds this service's machine-readable event contract so the
// running binary can serve it (GET /asyncapi.yaml) without depending on the
// source tree being present at runtime. Dockerfile's final stage copies only
// the compiled binaries into the distroless image; embedding avoids the
// problem entirely.
package api

import _ "embed"

// AsyncAPISpec is the verbatim contents of asyncapi.yaml, embedded at
// compile time.
//
//go:embed asyncapi.yaml
var AsyncAPISpec []byte
