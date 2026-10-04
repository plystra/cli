package bootstrapgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/implementationinventory"
	"go.yaml.in/yaml/v3"
)

// ConstructorConfigurationInput carries private final configuration only while
// planning. Rendering retains its build-visible projection, never runtime values.
type ConstructorConfigurationInput struct {
	Symbol string
	Schema implementationinventory.Configuration
	YAML   []byte
}

func (ConstructorConfigurationInput) String() string   { return "<private-constructor-configuration>" }
func (ConstructorConfigurationInput) GoString() string { return "<private-constructor-configuration>" }

func compileConstructorFields(fields []implementationinventory.ConfigurationField) []constructorconfig.Field {
	result := make([]constructorconfig.Field, len(fields))
	for i, field := range fields {
		result[i] = constructorconfig.Field{
			Name: field.Name(), GoName: field.GoName(), Required: field.Required(),
			BuildVisible: field.BuildVisible(), HasDefault: field.HasDefault(),
			Default: field.DefaultJSON(), Value: compileConstructorValue(field.Value()),
		}
	}
	return result
}

func compileConstructorValue(value implementationinventory.ConfigurationValue) constructorconfig.Schema {
	s := constructorconfig.Schema{Kind: string(value.Kind()), Fields: compileConstructorFields(value.Fields())}
	s.Bits, _ = value.NumericBits()
	if value.PlatformSized() {
		s.Bits = 32
	}
	if length, fixed := value.ArrayLength(); fixed {
		s.Length = &length
	}
	if element, exists := value.Element(); exists {
		compiled := compileConstructorValue(element)
		s.Element = &compiled
	}
	return s
}

func compileConstructorConfiguration(input ConstructorConfigurationInput) (constructorconfig.Schema, string, error) {
	schema := constructorconfig.Schema{Kind: "object", Fields: compileConstructorFields(input.Schema.Fields())}
	var node *yaml.Node
	if len(input.YAML) != 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(input.YAML, &doc); err != nil || len(doc.Content) != 1 {
			return schema, "", fmt.Errorf("invalid runtime constructor input %q", input.Symbol)
		}
		node = doc.Content[0]
	}
	normalized, err := constructorconfig.Normalize(schema, node)
	if err != nil {
		return schema, "", fmt.Errorf("constructor %s: %w", input.Symbol, err)
	}
	public, err := constructorconfig.PublicJSON(schema, normalized)
	if err != nil {
		return schema, "", err
	}
	digest := sha256.Sum256(public)
	return schema, hex.EncodeToString(digest[:]), nil
}

func renderConstructorConfiguration(inputs []ConstructorConfigurationInput, order []string, resources []ResourceConfigurationInput, instances []ResourceInstanceInput, resourceOrder []string) (string, error) {
	indices := make(map[string]int, len(order))
	for i, symbol := range order {
		indices[symbol] = i
	}
	inputs = append([]ConstructorConfigurationInput(nil), inputs...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Symbol < inputs[j].Symbol })
	var source strings.Builder
	source.WriteString(`
type runtimeConstructorBinding struct {
	symbol string
	resource string
	schema constructorconfig.Schema
	expected string
	target any
	node *yaml.Node
}

func (binding runtimeConstructorBinding) owner() string {
	if binding.resource != "" { return "Resource " + binding.resource }
	return "constructor " + binding.symbol
}

func runtimeConstructorBindings(configuration *applicationassembly.ConstructorConfiguration) ([]runtimeConstructorBinding, error) {
	bindings := []runtimeConstructorBinding{}
`)
	for i, input := range inputs {
		index, exists := indices[input.Symbol]
		if !exists || i > 0 && inputs[i-1].Symbol == input.Symbol {
			return "", fmt.Errorf("invalid runtime constructor binding %q", input.Symbol)
		}
		schema, publicDigest, err := compileConstructorConfiguration(input)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&source, `
	{
		var schema constructorconfig.Schema
		if err := json.Unmarshal([]byte(%s), &schema); err != nil { return nil, ErrRuntimeConfiguration }
		if err := constructorconfig.BindDefaults(&schema, reflect.TypeOf(configuration.Config%d)); err != nil { return nil, fmt.Errorf("%%w: compiled constructor Config schema changed; regenerate and rebuild", ErrRuntimeCompatibility) }
		bindings = append(bindings, runtimeConstructorBinding{symbol: %s, schema: schema, expected: %s, target: &configuration.Config%d})
	}
`, strconv.Quote(string(encoded)), index, strconv.Quote(input.Symbol), strconv.Quote(publicDigest), index)
	}
	resourceIndices, err := planResourceOrder(instances, resourceOrder)
	if err != nil {
		return "", err
	}
	providers := make(map[string]string, len(instances))
	for _, instance := range instances {
		providers[instance.Name] = instance.Provider
	}
	resources = append([]ResourceConfigurationInput(nil), resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })
	for i, input := range resources {
		index, exists := resourceIndices[input.Name]
		if !exists || providers[input.Name] != input.Provider || i > 0 && resources[i-1].Name == input.Name {
			return "", ErrInvalidOptions
		}
		schema, digest, err := compileConstructorConfiguration(ConstructorConfigurationInput{Symbol: input.Provider, Schema: input.Schema, YAML: input.YAML})
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(schema)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&source, `
	{
		var schema constructorconfig.Schema
		if err := json.Unmarshal([]byte(%s), &schema); err != nil { return nil, ErrRuntimeConfiguration }
		if err := constructorconfig.BindDefaults(&schema, reflect.TypeOf(configuration.ResourceConfig%d)); err != nil { return nil, fmt.Errorf("%%w: compiled Resource Config schema changed; regenerate and rebuild", ErrRuntimeCompatibility) }
		bindings = append(bindings, runtimeConstructorBinding{symbol: %s, resource: %s, schema: schema, expected: %s, target: &configuration.ResourceConfig%d})
	}
`, strconv.Quote(string(encoded)), index, strconv.Quote(input.Provider), strconv.Quote(input.Name), strconv.Quote(digest), index)
	}
	source.WriteString(`
	return bindings, nil
}

func runtimeConstructorSchema(symbol string) (constructorconfig.Schema, bool, error) {
	var configuration applicationassembly.ConstructorConfiguration
	bindings, err := runtimeConstructorBindings(&configuration)
	if err != nil { return constructorconfig.Schema{}, false, err }
	for _, binding := range bindings {
		if binding.resource == "" && binding.symbol == symbol { return binding.schema, true, nil }
	}
	return constructorconfig.Schema{}, false, nil
}

type runtimePreparedConfiguration struct {
	configuration applicationassembly.ConstructorConfiguration
	bindings []runtimeConstructorBinding
	legacyDocument []byte
}

func prepareRuntimeConstructorConfiguration(document []byte) (*runtimePreparedConfiguration, error) {
	prepared := &runtimePreparedConfiguration{}
	bindings, err := runtimeConstructorBindings(&prepared.configuration)
	if err != nil { return nil, err }
	root, err := decodeRuntimeDocument(document, "effective configuration")
	if err != nil { return nil, err }
	fields, err := runtimeMapping(root, "configuration", nil)
	if err != nil { return nil, err }
	objects, err := runtimeOptionalMapping(fields["config"], "config", nil)
	if err != nil { return nil, err }
	resources, err := runtimeOptionalMapping(fields["resources"], "resources", runtimeKeySet("instances", "bind"))
	if err != nil { return nil, err }
	instances, err := runtimeOptionalMapping(resources["instances"], "resources.instances", nil)
	if err != nil { return nil, err }
	for i := range bindings {
		binding := &bindings[i]
		node := objects[binding.symbol]
		if binding.resource != "" {
			instance, err := runtimeMapping(instances[binding.resource], "Resource instance", runtimeKeySet("use", "config"))
			if err != nil { return nil, err }
			provider, err := runtimeString(instance["use"])
			if err != nil || provider != binding.symbol { return nil, ErrRuntimeCompatibility }
			node = instance["config"]
		}
		binding.node, err = constructorconfig.Normalize(binding.schema, node)
		if err != nil { return nil, fmt.Errorf("%w: %s: %w", ErrRuntimeConfiguration, binding.owner(), err) }
		public, err := constructorconfig.PublicJSON(binding.schema, binding.node)
		if err != nil { return nil, ErrRuntimeConfiguration }
		publicDigest := sha256.Sum256(public)
		if hex.EncodeToString(publicDigest[:]) != binding.expected {
			if binding.resource == "" {
				return nil, fmt.Errorf("%w: build-visible constructor configuration changed for %s; rebuild with the same selector", ErrRuntimeCompatibility, binding.symbol)
			}
			return nil, fmt.Errorf("%w: build-visible configuration changed for %s; rebuild with the same selector", ErrRuntimeCompatibility, binding.owner())
		}
		if binding.resource == "" { delete(objects, binding.symbol) }
	}
	fields["config"] = runtimeMappingNode(objects)
	delete(fields, "resources")
	prepared.legacyDocument, err = encodeRuntimeDocument(runtimeMappingNode(fields))
	if err != nil { return nil, err }
	prepared.bindings = bindings
	return prepared, nil
}

func (p *runtimePreparedConfiguration) resolve(ctx context.Context, resolver *kernelconfiguration.Resolver) error {
	for _, binding := range p.bindings {
		if err := constructorconfig.Bind(ctx, resolver, binding.schema, binding.node, binding.target); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrRuntimeConfiguration, binding.owner(), err)
		}
	}
	return nil
}
`)
	return source.String(), nil
}
