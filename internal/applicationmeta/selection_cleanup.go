package applicationmeta

import (
	"errors"
	"fmt"
	"go/token"
	"sort"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// ErrCleanupSelectionOwnership reports an invalid exact cleanup plan or document.
var ErrCleanupSelectionOwnership = errors.New("clean up selection ownership")

// ConstructorConfigurationRemoval removes one top-level config entry whose
// previous owner the caller proved is absent from the final ownership graph.
// Tombstone suppresses inherited configuration; false deletes only the local entry.
type ConstructorConfigurationRemoval struct {
	Constructor constructorsymbol.Symbol
	Tombstone   bool
}

// ResourceBindingRemoval removes one exact resources.bind leaf. Namespace is
// "implementations" or "instances"; Consumer is respectively a constructor symbol
// or instance name. Tombstone suppresses inheritance instead of deleting locally.
// The caller must prove this binding is obsolete, never infer a replacement target.
type ResourceBindingRemoval struct {
	Namespace     string
	Consumer      string
	ParameterName string
	Tombstone     bool
}

// CleanupSelectionOwnership applies only caller-proven orphan configuration and
// obsolete binding removals. It neither discovers owners nor removes containers,
// instances, selections, or other leaves. Run it only inside the caller's complete
// selection transaction; intermediate documents need not resolve independently.
func CleanupSelectionOwnership(data []byte, configurations []ConstructorConfigurationRemoval, bindings []ResourceBindingRemoval) ([]byte, bool, error) {
	return cleanupSelectionOwnership(data, configurations, bindings, false)
}

// CleanupSelectionOwnershipOverlay applies exact cleanup to one sparse overlay.
// Inherited removals materialize only leaf tombstones, never inherited values.
func CleanupSelectionOwnershipOverlay(data []byte, configurations []ConstructorConfigurationRemoval, bindings []ResourceBindingRemoval) ([]byte, bool, error) {
	return cleanupSelectionOwnership(data, configurations, bindings, true)
}

func cleanupSelectionOwnership(data []byte, configurations []ConstructorConfigurationRemoval, bindings []ResourceBindingRemoval, overlay bool) ([]byte, bool, error) {
	configurations, bindings, err := selectionRemovals(configurations, bindings)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrCleanupSelectionOwnership, err)
	}
	return editSelectionDocument(data, overlay, ErrCleanupSelectionOwnership, func(root *yaml.Node) (bool, error) {
		changed := false
		for _, removal := range configurations {
			removed, err := removeSelectionLeaf(root, []string{"config", removal.Constructor.String()}, removal.Tombstone)
			if err != nil {
				return false, err
			}
			changed = changed || removed
		}
		for _, removal := range bindings {
			removed, err := removeSelectionLeaf(root, resourceBindingRemovalPath(removal), removal.Tombstone)
			if err != nil {
				return false, err
			}
			changed = changed || removed
		}
		return changed, nil
	})
}

func selectionRemovals(configurations []ConstructorConfigurationRemoval, bindings []ResourceBindingRemoval) ([]ConstructorConfigurationRemoval, []ResourceBindingRemoval, error) {
	configurations = append([]ConstructorConfigurationRemoval(nil), configurations...)
	bindings = append([]ResourceBindingRemoval(nil), bindings...)
	seenConfigurations := make(map[constructorsymbol.Symbol]bool, len(configurations))
	for _, removal := range configurations {
		if removal.Constructor.String() == "" || seenConfigurations[removal.Constructor] {
			return nil, nil, errors.New("empty or duplicate constructor configuration removal")
		}
		seenConfigurations[removal.Constructor] = true
	}
	seenBindings := make(map[[3]string]bool, len(bindings))
	for _, removal := range bindings {
		key := [3]string{removal.Namespace, removal.Consumer, removal.ParameterName}
		if removal.ParameterName == "_" || !token.IsIdentifier(removal.ParameterName) || seenBindings[key] {
			return nil, nil, errors.New("invalid or duplicate Resource binding removal")
		}
		switch removal.Namespace {
		case "implementations":
			if _, err := constructorsymbol.Parse(removal.Consumer); err != nil {
				return nil, nil, errors.New("invalid Implementation binding consumer")
			}
		case "instances":
			if resourcename.Check(removal.Consumer) != nil {
				return nil, nil, errors.New("invalid Resource binding consumer")
			}
		default:
			return nil, nil, errors.New("invalid Resource binding namespace")
		}
		seenBindings[key] = true
	}
	sort.Slice(configurations, func(i, j int) bool {
		return configurations[i].Constructor.String() < configurations[j].Constructor.String()
	})
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Consumer != b.Consumer {
			return a.Consumer < b.Consumer
		}
		return a.ParameterName < b.ParameterName
	})
	return configurations, bindings, nil
}

func resourceBindingRemovalPath(removal ResourceBindingRemoval) []string {
	return []string{"resources", "bind", removal.Namespace, removal.Consumer, removal.ParameterName}
}

func removeSelectionLeaf(root *yaml.Node, path []string, tombstone bool) (bool, error) {
	parent := root
	for _, name := range path[:len(path)-1] {
		parent = mappingChild(parent, name)
	}
	key := path[len(path)-1]
	value := mappingChild(parent, key)
	if !tombstone {
		if value == nil {
			return false, nil
		}
		removeMappingValue(parent, key)
		return true, nil
	}
	if isRemovalMapping(value) {
		return false, nil
	}
	if err := setKeyedConfigurationDecision(root, path[:len(path)-1], key, configurationRemovalYAMLNode()); err != nil {
		return false, err
	}
	return true, nil
}

func editSelectionDocument(data []byte, overlay bool, operation error, edit func(*yaml.Node) (bool, error)) ([]byte, bool, error) {
	parse := Parse
	if overlay {
		parse = func(input []byte) (Manifest, error) {
			return ParseOverlaySource("plystra.<environment>.yaml", input)
		}
	}
	// Parser errors can contain unknown authored keys. The editor reports the
	// manifest sentinel without forwarding those private input details.
	if _, err := parse(data); err != nil {
		return nil, false, fmt.Errorf("%w: %w", operation, ErrInvalidManifest)
	}
	root, err := decodeDocument(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", operation, ErrInvalidManifest)
	}
	changed, err := edit(root)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", operation, err)
	}
	if !changed {
		return append([]byte(nil), data...), false, nil
	}
	updated, err := encodeConfigurationDocument(root)
	if err != nil {
		return nil, false, fmt.Errorf("%w: cannot encode application document", operation)
	}
	if _, err := parse(updated); err != nil {
		return nil, false, fmt.Errorf("%w: updated document is invalid: %w", operation, ErrInvalidManifest)
	}
	return updated, true, nil
}
