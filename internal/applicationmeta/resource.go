package applicationmeta

import (
	"fmt"
	"go/token"
	"log/slog"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// ResourceInstance is one named provider and its private instance-owned Config.
// Parsed layers may omit Provider to inherit it during Compose.
type ResourceInstance struct {
	name                      string
	provider                  constructorsymbol.Symbol
	hasProvider               bool
	providerSource            string
	providerDeclarationSource ConfigurationDeclarationSource
	unboundConfiguration      bool
	source                    string
	declarationSource         ConfigurationDeclarationSource
	configuration             []resourceConfigurationLayer
	yaml                      []byte
}

type resourceConfigurationLayer struct {
	yaml              []byte
	remove            bool
	source            string
	declarationSource ConfigurationDeclarationSource
}

// Name returns the exact process-local instance name.
func (r ResourceInstance) Name() string { return r.name }

// Provider returns the exact provider constructor, or zero in an unbound delta.
func (r ResourceInstance) Provider() constructorsymbol.Symbol { return r.provider }

// ProviderSource returns the exact effective use declaration, independently
// of later config-only instance declarations.
func (r ResourceInstance) ProviderSource() string { return r.providerSource }

// ProviderDeclarationSource returns the exact selected use key span.
func (r ResourceInstance) ProviderDeclarationSource() ConfigurationDeclarationSource {
	return r.providerDeclarationSource
}

// HasConfiguration reports whether the instance has an effective Config object.
func (r ResourceInstance) HasConfiguration() bool {
	return len(r.configuration) != 0 && !r.configuration[len(r.configuration)-1].remove
}

// ConfigurationYAML returns private defensive bytes. Compose supplies the fully
// typed merged object; a parsed layer supplies only its authored delta.
func (r ResourceInstance) ConfigurationYAML() []byte { return append([]byte(nil), r.yaml...) }

// Source returns the highest instance declaration's stable configuration path.
func (r ResourceInstance) Source() string { return r.source }

// DeclarationSource returns the owning instance declaration's exact key span.
func (r ResourceInstance) DeclarationSource() ConfigurationDeclarationSource {
	return r.declarationSource
}

func (ResourceInstance) String() string   { return "<redacted-resource-instance>" }
func (ResourceInstance) GoString() string { return "<redacted-resource-instance>" }
func (ResourceInstance) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-resource-instance>"))
}
func (ResourceInstance) LogValue() slog.Value {
	return slog.StringValue("<redacted-resource-instance>")
}

// ResourceBinding is one exact namespace/consumer/parameter address and target.
type ResourceBinding struct {
	namespace         string
	consumer          string
	parameter         string
	target            string
	source            string
	declarationSource ConfigurationDeclarationSource
}

// Namespace returns implementations or instances.
func (b ResourceBinding) Namespace() string { return b.namespace }

// Consumer returns the exact constructor symbol or consumer instance name.
func (b ResourceBinding) Consumer() string { return b.consumer }

// ParameterName returns the exact case-sensitive Go dependency identifier.
func (b ResourceBinding) ParameterName() string { return b.parameter }

// Target returns the selected Resource instance name.
func (b ResourceBinding) Target() string { return b.target }

// Source returns the stable binding leaf path.
func (b ResourceBinding) Source() string { return b.source }

// DeclarationSource returns the owning binding declaration's exact key span.
func (b ResourceBinding) DeclarationSource() ConfigurationDeclarationSource {
	return b.declarationSource
}

// ResourceInstances returns defensive name-sorted declarations. Only a composed
// Manifest has complete Resource configuration; ApplyOverlay retains sparse layers.
func (m Manifest) ResourceInstances() []ResourceInstance {
	return append([]ResourceInstance(nil), m.resourceInstances...)
}

// ResourceBindings returns defensive namespace/consumer/parameter-sorted bindings.
func (m Manifest) ResourceBindings() []ResourceBinding {
	return append([]ResourceBinding(nil), m.resourceBindings...)
}

func resourceInstancePath(name string) string { return fmt.Sprintf("resources.instances[%q]", name) }
func resourceBindingPath(b ResourceBinding) string {
	return fmt.Sprintf("resources.bind.%s[%q][%q]", b.namespace, b.consumer, b.parameter)
}

// ResourceMetadataError reports a closed Resource schema violation without
// retaining submitted keys or values that could contain private material.
type ResourceMetadataError struct {
	source ConfigurationDeclarationSource
	field  string
	rule   string
}

func (e *ResourceMetadataError) Error() string {
	return fmt.Sprintf("%s: %s %s", ErrInvalidManifest, e.field, e.rule)
}
func (*ResourceMetadataError) Unwrap() error                            { return ErrInvalidManifest }
func (e *ResourceMetadataError) Source() ConfigurationDeclarationSource { return e.source }
func (e *ResourceMetadataError) Field() string                          { return e.field }

func resourceLocation(source string, node *yaml.Node) ConfigurationDeclarationSource {
	location := ConfigurationDeclarationSource{path: source, line: 1, column: 1}
	if node != nil {
		location.line, location.column = node.Line, node.Column
	}
	return location
}

func resourceKeyNode(node *yaml.Node, name string) *yaml.Node {
	if node != nil && node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			if node.Content[index].Value == name {
				return node.Content[index]
			}
		}
	}
	return node
}

func resourceMetadataError(source, path, rule string, node *yaml.Node) error {
	return &ResourceMetadataError{source: resourceLocation(source, node), field: path, rule: rule}
}

func resourceMapping(source, path string, node *yaml.Node) (map[string]*yaml.Node, error) {
	values, err := safeConstructorConfigMapping(node)
	if err != nil {
		return nil, resourceMetadataError(source, path, "must be a mapping with unique string keys", node)
	}
	return values, nil
}

func parseResources(manifest *Manifest, node *yaml.Node) error {
	if node == nil {
		return nil
	}
	values, err := resourceMapping(manifest.source, "resources", node)
	if err != nil {
		return err
	}
	for _, key := range sortedNodeKeys(values) {
		if key != "instances" && key != "bind" {
			return resourceMetadataError(manifest.source, "resources", "contains an unknown field", resourceKeyNode(node, key))
		}
	}
	if values["instances"] != nil {
		if err := parseResourceInstances(manifest, values["instances"]); err != nil {
			return err
		}
	}
	if values["bind"] != nil {
		return parseResourceBindings(manifest, values["bind"])
	}
	return nil
}

func parseResourceInstances(manifest *Manifest, node *yaml.Node) error {
	values, err := resourceMapping(manifest.source, "resources.instances", node)
	if err != nil {
		return err
	}
	for _, name := range sortedNodeKeys(values) {
		key := resourceKeyNode(node, name)
		if resourcename.Check(name) != nil {
			return resourceMetadataError(manifest.source, "resources.instances", "contains an invalid instance name", key)
		}
		path := resourceInstancePath(name)
		r := ResourceInstance{name: name, source: manifest.source + " " + path, declarationSource: resourceLocation(manifest.source, key)}
		if isRemovalMapping(values[name]) {
			manifest.removedResourceInstances = append(manifest.removedResourceInstances, r)
			continue
		}
		fields, err := resourceMapping(manifest.source, path, values[name])
		if err != nil {
			return err
		}
		for _, field := range sortedNodeKeys(fields) {
			if field != "use" && field != "config" {
				return resourceMetadataError(manifest.source, path, "contains an unknown field", resourceKeyNode(values[name], field))
			}
		}
		if use := fields["use"]; use != nil {
			value, err := strictString(use)
			if err == nil {
				r.provider, err = constructorsymbol.Parse(value)
			}
			if err != nil {
				return resourceMetadataError(manifest.source, path+".use", "must be an exact provider constructor symbol", use)
			}
			r.hasProvider = true
			r.providerSource = r.source + ".use"
			r.providerDeclarationSource = resourceLocation(manifest.source, resourceKeyNode(values[name], "use"))
		}
		if config := fields["config"]; config != nil {
			fragment := resourceConfigurationLayer{source: r.source + ".config", declarationSource: resourceLocation(manifest.source, resourceKeyNode(values[name], "config"))}
			fragment.remove = isRemovalMapping(config)
			if !fragment.remove {
				if config.Kind != yaml.MappingNode || mappingChild(config, "$remove") != nil || validateUntypedConfigurationNode(config, &constructorConfigNormalizeState{}, 0) != nil {
					return resourceMetadataError(manifest.source, path+".config", "must be a bounded configuration mapping or {$remove: true}", config)
				}
				fragment.yaml, err = marshalConstructorConfigNode(config)
				if err != nil {
					return resourceMetadataError(manifest.source, path+".config", "cannot be normalized", config)
				}
				r.yaml = fragment.yaml
			}
			r.configuration = []resourceConfigurationLayer{fragment}
		}
		manifest.resourceInstances = append(manifest.resourceInstances, r)
	}
	return nil
}

func parseResourceBindings(manifest *Manifest, node *yaml.Node) error {
	values, err := resourceMapping(manifest.source, "resources.bind", node)
	if err != nil {
		return err
	}
	for _, namespace := range sortedNodeKeys(values) {
		if namespace != "implementations" && namespace != "instances" {
			return resourceMetadataError(manifest.source, "resources.bind", "contains an unknown namespace", resourceKeyNode(node, namespace))
		}
		path := "resources.bind." + namespace
		consumers, err := resourceMapping(manifest.source, path, values[namespace])
		if err != nil {
			return err
		}
		for _, consumer := range sortedNodeKeys(consumers) {
			var err error
			if namespace == "instances" {
				err = resourcename.Check(consumer)
			} else {
				_, err = constructorsymbol.Parse(consumer)
			}
			if err != nil {
				return resourceMetadataError(manifest.source, path, "contains an invalid consumer", resourceKeyNode(values[namespace], consumer))
			}
			consumerPath := fmt.Sprintf("%s[%q]", path, consumer)
			parameters, err := resourceMapping(manifest.source, consumerPath, consumers[consumer])
			if err != nil {
				return err
			}
			for _, parameter := range sortedNodeKeys(parameters) {
				key := resourceKeyNode(consumers[consumer], parameter)
				if parameter == "_" || !token.IsIdentifier(parameter) {
					return resourceMetadataError(manifest.source, consumerPath, "contains an invalid dependency parameter identifier", key)
				}
				binding := ResourceBinding{namespace: namespace, consumer: consumer, parameter: parameter, declarationSource: resourceLocation(manifest.source, key)}
				binding.source = manifest.source + " " + resourceBindingPath(binding)
				if isRemovalMapping(parameters[parameter]) {
					manifest.removedResourceBindings = append(manifest.removedResourceBindings, binding)
					continue
				}
				binding.target, err = strictString(parameters[parameter])
				if err != nil || resourcename.Check(binding.target) != nil {
					return resourceMetadataError(manifest.source, resourceBindingPath(binding), "must name one Resource instance or {$remove: true}", parameters[parameter])
				}
				manifest.resourceBindings = append(manifest.resourceBindings, binding)
			}
		}
	}
	return nil
}

func withResourceModule(manifest Manifest, module string) Manifest {
	instances := func(values []ResourceInstance) []ResourceInstance {
		values = append([]ResourceInstance(nil), values...)
		for index := range values {
			values[index].declarationSource.modulePath = module
			if values[index].providerSource != "" {
				values[index].providerDeclarationSource.modulePath = module
			}
			values[index].configuration = append([]resourceConfigurationLayer(nil), values[index].configuration...)
			for field := range values[index].configuration {
				values[index].configuration[field].declarationSource.modulePath = module
			}
		}
		return values
	}
	bindings := func(values []ResourceBinding) []ResourceBinding {
		values = append([]ResourceBinding(nil), values...)
		for index := range values {
			values[index].declarationSource.modulePath = module
		}
		return values
	}
	manifest.resourceInstances = instances(manifest.resourceInstances)
	manifest.removedResourceInstances = instances(manifest.removedResourceInstances)
	manifest.resourceBindings = bindings(manifest.resourceBindings)
	manifest.removedResourceBindings = bindings(manifest.removedResourceBindings)
	return manifest
}
