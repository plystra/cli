package constructorconfig

import _ "embed"

// Source is the exact validator and binder compiled by the CLI and emitted for startup.
//
//go:embed value.go
var Source string
