package applicationmeta

import (
	"fmt"
	"slices"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

// InterfaceRootsIdentity changes captured root membership without Config or
// intermediate encoding. The caller must still compose and validate real bytes.
func InterfaceRootsIdentity(manifest Manifest, roots []InterfaceRootEdit, overlay bool) (Manifest, error) {
	if manifest.source == "" || len(manifest.layers) != 0 || validateInterfaceRootEdits(roots) != nil {
		return Manifest{}, ErrEditConfiguration
	}
	for _, root := range roots {
		if root.PreserveRoot {
			continue
		}
		var err error
		manifest, err = interfaceRootIdentity(manifest, root.Interface, root.Root, root.Present, overlay)
		if err != nil {
			return Manifest{}, err
		}
	}
	return WithoutConstructorConfiguration(manifest), nil
}

func interfaceRootIdentity(manifest Manifest, id interfaceid.Identifier, root string, present, overlay bool) (Manifest, error) {
	if manifest.source == "" || len(manifest.layers) != 0 || id.String() == "" || root != "require" && root != "expose" {
		return Manifest{}, ErrEditConfiguration
	}
	manifest = WithoutConstructorConfiguration(manifest)
	if present && manifest.HasInterfaceRoot(id, root) {
		return manifest, nil
	}
	if root == "require" {
		requirements := make([]InterfaceRequirement, 0, len(manifest.interfaceRequirements)+1)
		for _, requirement := range manifest.interfaceRequirements {
			if requirement.id != id {
				requirements = append(requirements, requirement)
			}
		}
		manifest.interfaceRequirements = requirements
		manifest.removedInterfaceReqs = interfaceRemovalsExcept(manifest.removedInterfaceReqs, id)
		if present {
			path := "interfaces.require"
			if overlay && !manifest.completeInterfaceRequirements {
				path += ".add"
			}
			manifest.interfaceRequirements = append(manifest.interfaceRequirements, InterfaceRequirement{id: id, source: fmt.Sprintf("%s %s[%q]", manifest.source, path, id.String())})
			manifest.completeInterfaceRequirements = manifest.completeInterfaceRequirements || !overlay
		} else if overlay && !manifest.completeInterfaceRequirements {
			manifest.removedInterfaceReqs = append(manifest.removedInterfaceReqs, interfaceRemoval{id: id, source: fmt.Sprintf("%s interfaces.require.remove[%q]", manifest.source, id.String())})
		}
		slices.SortFunc(manifest.interfaceRequirements, func(a, b InterfaceRequirement) int { return strings.Compare(a.id.String(), b.id.String()) })
		slices.SortFunc(manifest.removedInterfaceReqs, func(a, b interfaceRemoval) int { return strings.Compare(a.id.String(), b.id.String()) })
	} else {
		exposures := make([]HTTPExposure, 0, len(manifest.httpExposures)+1)
		for _, exposure := range manifest.httpExposures {
			if exposure.id != id {
				exposures = append(exposures, exposure)
			}
		}
		manifest.httpExposures = exposures
		manifest.removedHTTPExposures = interfaceRemovalsExcept(manifest.removedHTTPExposures, id)
		source := fmt.Sprintf("%s http.expose[%q]", manifest.source, id.String())
		if present {
			manifest.httpExposures = append(manifest.httpExposures, HTTPExposure{
				id: id, transport: HTTPTransportConnect, source: source,
				declarationSource: ConfigurationDeclarationSource{modulePath: manifest.modulePath, path: manifest.source, line: 1, column: 1},
			})
		} else if overlay {
			manifest.removedHTTPExposures = append(manifest.removedHTTPExposures, interfaceRemoval{id: id, source: source})
		}
		slices.SortFunc(manifest.httpExposures, func(a, b HTTPExposure) int { return strings.Compare(a.id.String(), b.id.String()) })
		slices.SortFunc(manifest.removedHTTPExposures, func(a, b interfaceRemoval) int { return strings.Compare(a.id.String(), b.id.String()) })
	}
	return manifest, nil
}

// InterfaceRootEdit changes exact root membership. PreserveRoot retains an
// already-effective present root without materializing inherited intent.
type InterfaceRootEdit struct {
	Interface    interfaceid.Identifier
	Root         string
	Present      bool
	PreserveRoot bool
}

// RootConfigurationEdit combines root requests, proven ownership cleanup and
// disjoint field edits in one complete document.
type RootConfigurationEdit struct {
	Roots          []InterfaceRootEdit
	Configurations []ConstructorConfigurationRemoval
	Bindings       []ResourceBindingRemoval
	Values         []ConfigurationValueEdit
}

// EditRootConfiguration encodes only the complete candidate document.
func EditRootConfiguration(data []byte, edit RootConfigurationEdit, overlay bool) ([]byte, bool, error) {
	if validateInterfaceRootEdits(edit.Roots) != nil || len(edit.Values) > 0 && validateConfigurationEdits(edit.Values) != nil {
		return nil, false, ErrEditConfiguration
	}
	configurations, bindings, err := selectionRemovals(edit.Configurations, edit.Bindings)
	if err != nil {
		return nil, false, ErrEditConfiguration
	}
	for _, removal := range configurations {
		for _, value := range edit.Values {
			if len(value.names) >= 2 && value.names[0] == "config" && value.names[1] == removal.Constructor.String() {
				return nil, false, ErrEditConfiguration
			}
		}
	}
	return editSelectionDocument(data, overlay, ErrEditConfiguration, func(document *yaml.Node) (bool, error) {
		changed := false
		for _, root := range edit.Roots {
			if root.PreserveRoot {
				continue
			}
			applied, err := applyInterfaceRoot(document, root.Interface, root.Root, root.Present, overlay)
			if err != nil {
				return false, err
			}
			changed = changed || applied
		}
		cleaned, err := applySelectionRemovals(document, configurations, bindings)
		if err != nil {
			return false, err
		}
		configured, err := applyConfigurationEdits(document, edit.Values, overlay)
		return changed || cleaned || configured, err
	})
}

func validateInterfaceRootEdits(roots []InterfaceRootEdit) error {
	if len(roots) == 0 {
		return ErrEditConfiguration
	}
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		key := root.Root + ":" + root.Interface.String()
		if root.Interface.String() == "" || root.Root != "require" && root.Root != "expose" || root.PreserveRoot && !root.Present || seen[key] {
			return ErrEditConfiguration
		}
		seen[key] = true
	}
	return nil
}
