package modulepath

import _ "embed"

const (
	// RuntimeModulePath is the module used by generated Project identity validation.
	RuntimeModulePath = "golang.org/x/mod"
	// RuntimeModuleVersion is the minimum supported path-validation runtime.
	RuntimeModuleVersion = "v0.38.0"
)

// Source is the exact Project identity validator compiled by the CLI and emitted for startup.
//
//go:embed path.go
var Source string
