package applicationmeta

import "go.yaml.in/yaml/v3"

// WithRootMetadata is retained as a no-op boundary for callers that select a
// complete replacement. A replacement has no relationship metadata to inherit.
func WithRootMetadata(selected, root Manifest) Manifest {
	return selected
}

func validateRootEnvelope(root *yaml.Node, values map[string]*yaml.Node) error {
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "http", "timeouts", "capabilities", "interfaces", "config", "resources", "data":
		default:
			return invalid("unknown root field")
		}
	}
	return nil
}

func rootMapping(source string, root *yaml.Node) (map[string]*yaml.Node, error) {
	return mapping(root, "document")
}

// ParseRootMetadataSource reads only the mandatory root Project marker. The
// application values in a root document are intentionally excluded when a
// complete replacement is selected, but the enclosing YAML remains bounded,
// uniquely keyed, and limited to the known document envelope.
func ParseRootMetadataSource(source string, data []byte) (Manifest, error) {
	if source != "plystra.yaml" {
		return Manifest{}, invalid("root metadata must be read from plystra.yaml")
	}
	if err := validateConfigurationSource(source); err != nil {
		return Manifest{}, err
	}
	root, err := decodeDocument(data)
	if err != nil {
		return Manifest{}, err
	}
	values, err := rootMapping(source, root)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateRootEnvelope(root, values); err != nil {
		return Manifest{}, err
	}
	if err := validateUntypedConfigurationNode(root, &constructorConfigNormalizeState{}, 0); err != nil {
		return Manifest{}, invalid("document must contain valid YAML scalar values and unique string keys")
	}
	return Manifest{source: source, startupTimeout: DefaultStartupTimeout}, nil
}
