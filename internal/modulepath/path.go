// Package modulepath validates Go Module identities used at Plystra Project
// boundaries without weakening ordinary dependency path rules.
package modulepath

import (
	"strings"

	"golang.org/x/mod/module"
)

// CheckProject accepts a standard Go Module path or the single-component
// local module identity produced by plystra new when --module is omitted.
// Multi-component paths remain subject to standard Go Module path rules.
func CheckProject(value string) error {
	standardErr := module.CheckPath(value)
	if standardErr == nil {
		return nil
	}
	if strings.Contains(value, "/") {
		return standardErr
	}
	return module.CheckImportPath(value)
}
