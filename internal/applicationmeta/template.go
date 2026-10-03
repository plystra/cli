package applicationmeta

import (
	"fmt"

	"github.com/plystra/cli/internal/modulepath"
	"go.yaml.in/yaml/v3"
)

// TemplateMetadataError identifies the exact root relationship declaration.
// Invalid authored values are excluded from the error.
type TemplateMetadataError struct {
	source ConfigurationDeclarationSource
	rule   string
}

func (e *TemplateMetadataError) Error() string {
	return ErrInvalidManifest.Error() + ": template " + e.rule
}
func (*TemplateMetadataError) Unwrap() error                            { return ErrInvalidManifest }
func (e *TemplateMetadataError) Source() ConfigurationDeclarationSource { return e.source }

// Template returns the exact direct-template Go Module path.
func (m Manifest) Template() string { return m.template }

// TemplateSource returns the exact declaration key span, or zero when absent.
func (m Manifest) TemplateSource() ConfigurationDeclarationSource { return m.templateSource }

// WithRootMetadata retains only the root relationship when selecting a complete
// replacement. It never carries root application declarations into the selection.
func WithRootMetadata(selected, root Manifest) Manifest {
	selected.template, selected.templateSource = root.template, root.templateSource
	return selected
}

func parseTemplateRelationship(source string, root, node *yaml.Node, allowed bool) (string, ConfigurationDeclarationSource, error) {
	if node == nil {
		return "", ConfigurationDeclarationSource{}, nil
	}
	location := ConfigurationDeclarationSource{path: source, line: node.Line, column: node.Column}
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "template" {
			location.line, location.column = root.Content[index].Line, root.Content[index].Column
			break
		}
	}
	fail := func(rule string) (string, ConfigurationDeclarationSource, error) {
		return "", location, &TemplateMetadataError{source: location, rule: rule}
	}
	if !allowed {
		return fail("may be declared only in root plystra.yaml")
	}
	value, err := strictString(node)
	if err != nil || len(value) > 4096 || modulepath.CheckProject(value) != nil {
		return fail("must be one exact Go Module path, without a version or selector")
	}
	return value, location, nil
}

func validateRootEnvelope(root *yaml.Node, values map[string]*yaml.Node) error {
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "template", "http", "timeouts", "capabilities", "interfaces", "config", "resources", "data":
		default:
			return invalid("unknown key %q", key)
		}
	}
	return nil
}

func rootMapping(source string, root *yaml.Node) (map[string]*yaml.Node, error) {
	if root.Kind == yaml.MappingNode {
		seen := false
		for index := 0; index < len(root.Content); index += 2 {
			key := root.Content[index]
			if key.Kind == yaml.ScalarNode && key.Tag == "!!str" && key.Value == "template" {
				if seen {
					return nil, &TemplateMetadataError{source: ConfigurationDeclarationSource{path: source, line: key.Line, column: key.Column}, rule: "must be declared exactly once"}
				}
				seen = true
			}
		}
	}
	return mapping(root, "document")
}

// ParseRootMetadataSource reads root identity while excluding its application
// values. Even excluded values must be syntactically well-formed bounded YAML.
func ParseRootMetadataSource(source string, data []byte) (Manifest, error) {
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
	template, location, err := parseTemplateRelationship(source, root, values["template"], source == "plystra.yaml")
	if err != nil {
		return Manifest{}, err
	}
	if err := validateUntypedConfigurationNode(root, &constructorConfigNormalizeState{}, 0); err != nil {
		return Manifest{}, invalid("document must contain valid YAML scalar values and unique string keys")
	}
	return Manifest{source: source, template: template, templateSource: location, startupTimeout: DefaultStartupTimeout}, nil
}

// ParseTemplateSource reads a root reusable application layer. Process-local
// address and startup settings are excluded without interpreting their values.
func ParseTemplateSource(source string, data []byte) (Manifest, error) {
	if _, err := ParseRootMetadataSource(source, data); err != nil {
		return Manifest{}, err
	}
	root, err := decodeDocument(data)
	if err != nil {
		return Manifest{}, err
	}
	if err := stripProcessConfiguration(root); err != nil {
		return Manifest{}, err
	}
	values, err := mapping(root, "document")
	if err != nil {
		return Manifest{}, err
	}
	return parseManifestNode(source, root, values, false)
}

func stripProcessConfiguration(root *yaml.Node) error {
	if http := mappingChild(root, "http"); http != nil {
		fields, err := mapping(http, "http")
		if err != nil {
			return err
		}
		for _, key := range sortedNodeKeys(fields) {
			if key != "address" && key != "cors" && key != "expose" {
				return invalid("http contains an unknown field")
			}
		}
		removeMappingValue(http, "address")
		if len(http.Content) == 0 {
			removeMappingValue(root, "http")
		}
	}
	if timeouts := mappingChild(root, "timeouts"); timeouts != nil {
		fields, err := mapping(timeouts, "timeouts")
		if err != nil {
			return err
		}
		for key := range fields {
			if key != "startup" {
				return invalid("timeouts contains an unknown field")
			}
		}
		removeMappingValue(root, "timeouts")
	}
	return nil
}

// PrivateTemplateYAML returns normalized private reusable values, without
// template metadata, process settings, comments, or presentation.
func PrivateTemplateYAML(data []byte) ([]byte, error) {
	if _, err := ParseTemplateSource("plystra.yaml", data); err != nil {
		return nil, err
	}
	root, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	if err := stripProcessConfiguration(root); err != nil {
		return nil, err
	}
	removeMappingValue(root, "template")
	var normalize func(*yaml.Node)
	normalize = func(node *yaml.Node) {
		node.HeadComment, node.LineComment, node.FootComment = "", "", ""
		node.Style = 0
		if isNull(node) {
			node.Value = "null"
		}
		for _, child := range node.Content {
			normalize(child)
		}
		sortYAMLMapping(node)
	}
	normalize(root)
	result, err := marshalConstructorConfigNode(root)
	if err != nil {
		return nil, invalid("private template normalization failed")
	}
	if len(result) > MaximumSize {
		return nil, invalid("normalized template exceeds %d bytes", MaximumSize)
	}
	return result, nil
}

// SetTemplate writes exactly one root relationship without copying baseline
// values. Existing unrelated declarations and comments remain owned by the user.
func SetTemplate(data []byte, modulePath string) ([]byte, error) {
	if len(modulePath) > 4096 || modulepath.CheckProject(modulePath) != nil {
		return nil, invalid("template must be one exact Go Module path")
	}
	before, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if before.Template() == modulePath {
		return append([]byte(nil), data...), nil
	}
	root, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	setMappingValue(root, "template", stringYAMLNode(modulePath))
	result, err := encodeMaintainedDocument(root)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot encode template relationship", ErrInvalidManifest)
	}
	if _, err := Parse(result); err != nil {
		return nil, err
	}
	return result, nil
}
