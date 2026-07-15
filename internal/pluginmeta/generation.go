package pluginmeta

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/module"
)

const (
	// GenerationAPIV1 is the supported generation-extension protocol version.
	GenerationAPIV1 = "v1"
)

// ErrUnsupportedGenerationAPI reports a syntactically valid but unsupported
// generation-extension protocol version.
var ErrUnsupportedGenerationAPI = errors.New("unsupported generation API")

// Generation is one immutable trusted build-time extension declaration.
type Generation struct {
	api         string
	packagePath string
	activations []GenerationActivation
}

// API returns the exact generation protocol version.
func (g Generation) API() string { return g.api }

// Package returns the canonical plugin-relative Go package path.
func (g Generation) Package() string { return g.packagePath }

// Activations returns a defensive copy sorted by extension namespace.
func (g Generation) Activations() []GenerationActivation {
	return append([]GenerationActivation(nil), g.activations...)
}

// GenerationActivation associates one extension namespace with a canonical
// Capability provided by the declaring plugin.
type GenerationActivation struct {
	namespace  string
	capability capabilityid.Identifier
}

// Namespace returns the canonical lower-kebab extension namespace.
func (a GenerationActivation) Namespace() string { return a.namespace }

// Capability returns the exact canonical activation Capability.
func (a GenerationActivation) Capability() capabilityid.Identifier { return a.capability }

func parseGeneration(node *yaml.Node, provides []capabilityid.Identifier) (Generation, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return Generation{}, invalid("generation must be a mapping")
	}
	var apiNode, packageNode, activationsNode *yaml.Node
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return Generation{}, invalid("generation contains a non-string key")
		}
		key := keyNode.Value
		if _, duplicate := seen[key]; duplicate {
			return Generation{}, invalid("generation contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "api":
			apiNode = valueNode
		case "package":
			packageNode = valueNode
		case "activations":
			activationsNode = valueNode
		default:
			return Generation{}, invalid("generation contains unknown key %q", key)
		}
	}

	api, err := requiredGenerationString("api", apiNode)
	if err != nil {
		return Generation{}, err
	}
	if api != GenerationAPIV1 {
		return Generation{}, fmt.Errorf("%w: %w: generation.api %q is not supported; supported API is %q", ErrInvalidManifest, ErrUnsupportedGenerationAPI, api, GenerationAPIV1)
	}
	packagePath, err := requiredGenerationString("package", packageNode)
	if err != nil {
		return Generation{}, err
	}
	if err := validateGenerationPackage(packagePath); err != nil {
		return Generation{}, invalid("generation.package %q is not a confined canonical Go package path: %v", packagePath, err)
	}
	activations, err := parseGenerationActivations(activationsNode)
	if err != nil {
		return Generation{}, err
	}
	for _, activation := range activations {
		if !containsCapability(provides, activation.capability) {
			return Generation{}, invalid("generation activation namespace %q names capability %s, which the plugin does not provide", activation.namespace, activation.capability)
		}
	}
	return Generation{api: api, packagePath: packagePath, activations: activations}, nil
}

func requiredGenerationString(field string, node *yaml.Node) (string, error) {
	if node == nil {
		return "", invalid("generation.%s is required", field)
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
		return "", invalid("generation.%s must be a non-empty string", field)
	}
	return node.Value, nil
}

func validateGenerationPackage(value string) error {
	if !strings.HasPrefix(value, "./") {
		return errors.New("must start with ./")
	}
	relative := strings.TrimPrefix(value, "./")
	if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || strings.Contains(relative, "\\") {
		return errors.New("must be a clean relative slash-separated path")
	}
	if err := module.CheckImportPath("example.com/plystra-plugin/" + relative); err != nil {
		return fmt.Errorf("invalid Go import path: %v", err)
	}
	return nil
}

func parseGenerationActivations(node *yaml.Node) ([]GenerationActivation, error) {
	if node == nil {
		return nil, invalid("generation.activations is required")
	}
	if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, invalid("generation.activations must be a non-empty sequence")
	}
	activations := make([]GenerationActivation, 0, len(node.Content))
	seenNamespaces := make(map[string]struct{}, len(node.Content))
	for index, activationNode := range node.Content {
		activation, err := parseGenerationActivation(index, activationNode)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenNamespaces[activation.namespace]; duplicate {
			return nil, invalid("generation.activations contains duplicate namespace %q", activation.namespace)
		}
		seenNamespaces[activation.namespace] = struct{}{}
		activations = append(activations, activation)
	}
	sort.Slice(activations, func(left, right int) bool {
		return activations[left].namespace < activations[right].namespace
	})
	return activations, nil
}

func parseGenerationActivation(index int, node *yaml.Node) (GenerationActivation, error) {
	pathPrefix := fmt.Sprintf("generation.activations[%d]", index)
	if node == nil || node.Kind != yaml.MappingNode {
		return GenerationActivation{}, invalid("%s must be a mapping", pathPrefix)
	}
	var namespaceNode, capabilityNode *yaml.Node
	seen := make(map[string]struct{}, len(node.Content)/2)
	for contentIndex := 0; contentIndex < len(node.Content); contentIndex += 2 {
		keyNode, valueNode := node.Content[contentIndex], node.Content[contentIndex+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return GenerationActivation{}, invalid("%s contains a non-string key", pathPrefix)
		}
		key := keyNode.Value
		if _, duplicate := seen[key]; duplicate {
			return GenerationActivation{}, invalid("%s contains duplicate key %q", pathPrefix, key)
		}
		seen[key] = struct{}{}
		switch key {
		case "namespace":
			namespaceNode = valueNode
		case "capability":
			capabilityNode = valueNode
		default:
			return GenerationActivation{}, invalid("%s contains unknown key %q", pathPrefix, key)
		}
	}
	namespace, err := requiredActivationString(pathPrefix+".namespace", namespaceNode)
	if err != nil {
		return GenerationActivation{}, err
	}
	if !validGenerationNamespace(namespace) {
		return GenerationActivation{}, invalid("%s.namespace %q is not canonical lower kebab case", pathPrefix, namespace)
	}
	capabilityValue, err := requiredActivationString(pathPrefix+".capability", capabilityNode)
	if err != nil {
		return GenerationActivation{}, err
	}
	capability, err := capabilityid.Parse(capabilityValue)
	if err != nil {
		return GenerationActivation{}, invalid("%s.capability %q is not canonical", pathPrefix, capabilityValue)
	}
	return GenerationActivation{namespace: namespace, capability: capability}, nil
}

func requiredActivationString(field string, node *yaml.Node) (string, error) {
	if node == nil {
		return "", invalid("%s is required", field)
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
		return "", invalid("%s must be a non-empty string", field)
	}
	return node.Value, nil
}

func validGenerationNamespace(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}
