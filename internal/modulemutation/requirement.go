package modulemutation

import (
	"errors"
	"fmt"

	"golang.org/x/mod/modfile"
)

// Requirement describes one selected go.mod requirement.
type Requirement struct {
	version  string
	indirect bool
}

// Version returns the selected module version.
func (r Requirement) Version() string { return r.version }

// Indirect reports whether go.mod marks the requirement as indirect.
func (r Requirement) Indirect() bool { return r.indirect }

// FindRequirement reads the exact regular go.mod owned by root and returns the
// selected requirement for modulePath when one exists.
func FindRequirement(root, modulePath string) (Requirement, bool, error) {
	if modulePath == "" {
		return Requirement{}, false, errors.New("invalid Go Module path: value is empty")
	}
	files, err := captureModuleFiles(root)
	if err != nil {
		return Requirement{}, false, fmt.Errorf("capture module metadata: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", files["go.mod"].data, nil)
	if err != nil {
		return Requirement{}, false, fmt.Errorf("parse go.mod: %w", err)
	}
	for _, selected := range parsed.Require {
		if selected.Mod.Path == modulePath {
			return Requirement{version: selected.Mod.Version, indirect: selected.Indirect}, true, nil
		}
	}
	return Requirement{}, false, nil
}
